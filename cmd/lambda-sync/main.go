package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/wyattjs/whoop-sleep-onset/internal/dynamo"
	"github.com/wyattjs/whoop-sleep-onset/internal/whoop"
)

var (
	db       *dynamo.Client
	oauthCfg *whoop.OAuthConfig
)

func init() {
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = zerolog.New(os.Stdout).With().Timestamp().Logger()

	ctx := context.Background()

	var err error
	db, err = dynamo.New(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to init dynamo client")
	}

	oauthCfg, err = loadOAuthConfig(ctx)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to load oauth config")
	}
}

func handler(ctx context.Context, event events.DynamoDBEvent) error {
	var syncErrors int

	for _, record := range event.Records {
		if record.EventName != "INSERT" {
			continue
		}

		img := record.Change.NewImage

		pk, ok := img["PK"]
		if !ok {
			continue
		}
		// Only process webhook events (PK = WHOOPUSER#<id>, SK starts with WEBHOOK#)
		pkVal := pk.String()
		if !strings.HasPrefix(pkVal, "WHOOPUSER#") {
			continue
		}

		sk, ok := img["SK"]
		if !ok {
			continue
		}
		skVal := sk.String()
		if !strings.HasPrefix(skVal, "WEBHOOK#") {
			continue
		}

		eventType := ""
		if t, ok := img["type"]; ok {
			eventType = t.String()
		}

		whoopIDStr := strings.TrimPrefix(pkVal, "WHOOPUSER#")
		whoopID, err := strconv.Atoi(whoopIDStr)
		if err != nil {
			log.Error().Str("pk", pkVal).Msg("invalid whoop user ID in stream record")
			syncErrors++
			continue
		}

		logger := log.With().Int("whoop_id", whoopID).Str("event_type", eventType).Logger()
		logger.Info().Msg("processing webhook stream record")

		user, err := db.GetUserByWhoopID(ctx, whoopID)
		if err != nil {
			logger.Error().Err(err).Msg("failed to look up user")
			syncErrors++
			continue
		}

		accessToken, err := ensureValidToken(ctx, user)
		if err != nil {
			logger.Error().Err(err).Msg("failed to refresh token")
			syncErrors++
			continue
		}

		client := &whoop.Client{AccessToken: accessToken}
		now := time.Now().UTC()
		start := now.Add(-24 * time.Hour)

		switch eventType {
		case "sleep.updated", "sleep.created":
			if err := syncSleep(ctx, client, user.PK, start, now); err != nil {
				logger.Error().Err(err).Msg("failed to sync sleep")
				syncErrors++
			}
		case "recovery.updated", "recovery.created":
			if err := syncRecovery(ctx, client, user.PK, start, now); err != nil {
				logger.Error().Err(err).Msg("failed to sync recovery")
				syncErrors++
			}
		default:
			logger.Info().Msg("unhandled webhook type, skipping")
		}
	}

	if syncErrors > 0 {
		log.Warn().Int("errors", syncErrors).Msg("sync completed with errors")
	}
	return nil
}

func ensureValidToken(ctx context.Context, user *dynamo.User) (string, error) {
	if time.Now().Before(user.TokenExpiry.Add(-5 * time.Minute)) {
		return user.AccessToken, nil
	}

	tokens, err := whoop.RefreshAccessToken(ctx, oauthCfg, user.RefreshToken)
	if err != nil {
		return "", fmt.Errorf("token refresh failed: %w", err)
	}

	user.AccessToken = tokens.AccessToken
	user.RefreshToken = tokens.RefreshToken
	user.TokenExpiry = whoop.TokenExpiry(tokens)

	if err := db.PutUser(ctx, user); err != nil {
		return "", fmt.Errorf("failed to persist refreshed tokens: %w", err)
	}

	return tokens.AccessToken, nil
}

func syncSleep(ctx context.Context, client *whoop.Client, userPK string, start, end time.Time) error {
	records, err := client.GetSleep(ctx, start, end)
	if err != nil {
		return err
	}

	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("failed to marshal sleep record: %w", err)
		}
		sk := fmt.Sprintf("SLEEP#%s", r.Start.Format(time.RFC3339))
		if err := db.PutSyncRecord(ctx, userPK, sk, string(data)); err != nil {
			return fmt.Errorf("failed to store sleep record %d: %w", r.ID, err)
		}
	}

	log.Info().Str("user", userPK).Int("count", len(records)).Msg("synced sleep")
	return nil
}

func syncRecovery(ctx context.Context, client *whoop.Client, userPK string, start, end time.Time) error {
	records, err := client.GetRecovery(ctx, start, end)
	if err != nil {
		return err
	}

	for _, r := range records {
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("failed to marshal recovery record: %w", err)
		}
		sk := fmt.Sprintf("RECOVERY#%s", r.CreatedAt.Format(time.RFC3339))
		if err := db.PutSyncRecord(ctx, userPK, sk, string(data)); err != nil {
			return fmt.Errorf("failed to store recovery record %d: %w", r.CycleID, err)
		}
	}

	log.Info().Str("user", userPK).Int("count", len(records)).Msg("synced recovery")
	return nil
}

func loadOAuthConfig(ctx context.Context) (*whoop.OAuthConfig, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	client := secretsmanager.NewFromConfig(cfg)
	secretName := os.Getenv("SECRET_NAME")

	result, err := client.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{
		SecretId: &secretName,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get secret: %w", err)
	}

	var secrets map[string]string
	if err := json.Unmarshal([]byte(*result.SecretString), &secrets); err != nil {
		return nil, fmt.Errorf("failed to parse secret: %w", err)
	}

	return &whoop.OAuthConfig{
		ClientID:     secrets["WHOOP_CLIENT_ID"],
		ClientSecret: secrets["WHOOP_CLIENT_SECRET"],
	}, nil
}

func main() {
	lambda.Start(handler)
}
