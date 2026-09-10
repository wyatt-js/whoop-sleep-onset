package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
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
	db       syncStore
	oauthCfg *whoop.OAuthConfig
)

type syncStore interface {
	GetUserByWhoopID(context.Context, int) (*dynamo.User, error)
	PutUser(context.Context, *dynamo.User) error
	PutSyncRecord(context.Context, string, string, string) error
	DeleteSyncByID(context.Context, string, string, string) error
}

func initialize() {
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

		id, ok := img["whoop_id"]
		if !ok || id.String() == "" {
			syncErrors++
			continue
		}
		var client *whoop.Client
		if eventType == "sleep.updated" || eventType == "recovery.updated" {
			accessToken, err := ensureValidToken(ctx, user)
			if err != nil {
				logger.Error().Err(err).Msg("failed to refresh token")
				syncErrors++
				continue
			}
			client = &whoop.Client{AccessToken: accessToken}
		}
		if err := syncEvent(ctx, client, user.PK, eventType, id.String()); err != nil {
			logger.Error().Err(err).Msg("failed to sync webhook object")
			syncErrors++
		}

	}

	if syncErrors > 0 {
		return fmt.Errorf("%d webhook records failed; retry batch", syncErrors)
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

func syncEvent(ctx context.Context, client *whoop.Client, userPK, eventType, id string) error {
	switch eventType {
	case "sleep.deleted":
		if err := db.DeleteSyncByID(ctx, userPK, "SLEEP#", id); err != nil {
			return err
		}
		return db.DeleteSyncByID(ctx, userPK, "RECOVERY#", id)
	case "recovery.deleted":
		return db.DeleteSyncByID(ctx, userPK, "RECOVERY#", id)
	case "sleep.updated", "recovery.updated":
	default:
		return nil
	}
	sleep, err := client.GetSleepByID(ctx, id)
	if err != nil {
		var apiErr *whoop.HTTPError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			if err := db.DeleteSyncByID(ctx, userPK, "SLEEP#", id); err != nil {
				return err
			}
			return db.DeleteSyncByID(ctx, userPK, "RECOVERY#", id)
		}
		return err
	}
	if err := storeRecord(ctx, userPK, "SLEEP#"+sleep.ID, sleep); err != nil {
		return err
	}
	if eventType == "sleep.updated" {
		return nil
	}
	recovery, err := client.GetRecoveryForCycle(ctx, sleep.CycleID)
	if err != nil {
		var apiErr *whoop.HTTPError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			return db.DeleteSyncByID(ctx, userPK, "RECOVERY#", id)
		}
		return err
	}
	if recovery.SleepID != sleep.ID {
		return fmt.Errorf("recovery does not match webhook sleep")
	}
	return storeRecord(ctx, userPK, "RECOVERY#"+recovery.SleepID, recovery)
}

func storeRecord(ctx context.Context, userPK, sk string, record any) error {
	data, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return db.PutSyncRecord(ctx, userPK, sk, string(data))
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

	if result.SecretString == nil {
		return nil, fmt.Errorf("secret must contain a JSON string")
	}
	var secrets map[string]string
	if err := json.Unmarshal([]byte(*result.SecretString), &secrets); err != nil {
		return nil, fmt.Errorf("failed to parse secret: %w", err)
	}

	if secrets["WHOOP_CLIENT_ID"] == "" || secrets["WHOOP_CLIENT_SECRET"] == "" {
		return nil, fmt.Errorf("WHOOP client credentials are missing from secret")
	}
	return &whoop.OAuthConfig{
		ClientID:     secrets["WHOOP_CLIENT_ID"],
		ClientSecret: secrets["WHOOP_CLIENT_SECRET"],
		RedirectURI:  os.Getenv("WHOOP_REDIRECT_URI"),
	}, nil
}

func main() {
	initialize()
	lambda.Start(handler)
}
