package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
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

	pendingStates = map[string]time.Time{}

	windowStart = 21
	windowEnd   = 3

	userTZ *time.Location
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

	tzName := os.Getenv("USER_TIMEZONE")
	if tzName == "" {
		tzName = "America/New_York"
	}
	userTZ, err = time.LoadLocation(tzName)
	if err != nil {
		log.Fatal().Err(err).Str("tz", tzName).Msg("failed to load timezone")
	}
}

func handler(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	path := req.RequestContext.HTTP.Path
	method := req.RequestContext.HTTP.Method

	log.Info().Str("method", method).Str("path", path).Msg("incoming request")

	switch {
	case method == "GET" && path == "/auth/whoop/start":
		return handleAuthStart(ctx, req)
	case method == "GET" && path == "/auth/whoop/callback":
		return handleAuthCallback(ctx, req)
	case method == "POST" && path == "/phone-lock":
		return handlePhoneLock(ctx, req)
	case method == "POST" && path == "/webhook/whoop":
		return handleWhoopWebhook(ctx, req)
	case method == "GET" && path == "/last":
		return handleLast(ctx, req)
	default:
		return respond(http.StatusNotFound, map[string]string{"error": "not found"})
	}
}

func handleAuthStart(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	state, err := whoop.GenerateState()
	if err != nil {
		log.Error().Err(err).Msg("failed to generate state")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to generate state"})
	}

	pendingStates[state] = time.Now().Add(10 * time.Minute)

	authURL := whoop.BuildAuthURL(oauthCfg, state)

	log.Info().Msg("redirecting to WHOOP OAuth")

	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusFound,
		Headers: map[string]string{
			"Location": authURL,
		},
	}, nil
}

func handleAuthCallback(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if errParam := req.QueryStringParameters["error"]; errParam != "" {
		log.Error().Str("error", errParam).Msg("oauth error from WHOOP")
		return respondHTML(http.StatusBadRequest, fmt.Sprintf(
			"<h1>Authentication Failed</h1><p>WHOOP returned an error: %s</p>", errParam,
		))
	}

	state := req.QueryStringParameters["state"]
	expiry, exists := pendingStates[state]
	if !exists || time.Now().After(expiry) {
		delete(pendingStates, state)
		return respondHTML(http.StatusBadRequest, "<h1>Authentication Failed</h1><p>Invalid or expired state.</p>")
	}
	delete(pendingStates, state)

	code := req.QueryStringParameters["code"]
	if code == "" {
		return respondHTML(http.StatusBadRequest, "<h1>Authentication Failed</h1><p>Missing authorization code.</p>")
	}

	tokens, err := whoop.ExchangeCode(ctx, oauthCfg, code)
	if err != nil {
		log.Error().Err(err).Msg("token exchange failed")
		return respondHTML(http.StatusInternalServerError, "<h1>Authentication Failed</h1><p>Could not exchange authorization code.</p>")
	}

	profile, err := whoop.GetProfile(ctx, tokens.AccessToken)
	if err != nil {
		log.Error().Err(err).Msg("failed to fetch WHOOP profile")
		return respondHTML(http.StatusInternalServerError, "<h1>Authentication Failed</h1><p>Could not fetch your WHOOP profile.</p>")
	}

	bearerToken, err := generateBearerToken()
	if err != nil {
		log.Error().Err(err).Msg("failed to generate bearer token")
		return respondHTML(http.StatusInternalServerError, "<h1>Authentication Failed</h1><p>Internal error.</p>")
	}

	user := &dynamo.User{
		PK:           fmt.Sprintf("USER#%d", profile.UserID),
		SK:           "PROFILE",
		WhoopUserID:  profile.UserID,
		AccessToken:  tokens.AccessToken,
		RefreshToken: tokens.RefreshToken,
		TokenExpiry:  whoop.TokenExpiry(tokens),
		BearerToken:  bearerToken,
	}

	if err := db.PutUser(ctx, user); err != nil {
		log.Error().Err(err).Msg("failed to store user")
		return respondHTML(http.StatusInternalServerError, "<h1>Authentication Failed</h1><p>Could not save your account.</p>")
	}

	log.Info().Int("whoop_user_id", profile.UserID).Msg("user authenticated and stored")

	html := fmt.Sprintf(`
		<html>
		<body style="font-family: sans-serif; max-width: 600px; margin: 50px auto; text-align: center;">
			<h1>Authenticated!</h1>
			<p>Welcome, %s.</p>
			<p>Your token:</p>
			<code style="background: #f0f0f0; padding: 12px 24px; font-size: 18px; display: inline-block; border-radius: 4px; user-select: all;">%s</code>
			<p style="margin-top: 24px;">Run this in your terminal:</p>
			<code style="background: #f0f0f0; padding: 8px 16px; display: inline-block; border-radius: 4px;">sleeponset configure --token %s</code>
			<p style="margin-top: 24px; color: #666;">You can close this tab.</p>
		</body>
		</html>
	`, profile.FirstName, bearerToken, bearerToken)

	return respondHTML(http.StatusOK, html)
}

func handlePhoneLock(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, err := authenticateRequest(ctx, req)
	if err != nil {
		return respond(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	var body struct {
		LockedAt time.Time `json:"locked_at"`
	}

	if req.Body != "" {
		if err := json.Unmarshal([]byte(req.Body), &body); err != nil {
			log.Error().Err(err).Msg("failed to parse phone-lock body")
			return respond(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}

	if body.LockedAt.IsZero() {
		body.LockedAt = time.Now().UTC()
	}

	hour := body.LockedAt.In(userTZ).Hour()
	if !isNightHour(hour) {
		log.Info().Int("hour", hour).Msg("phone-lock outside night window, ignoring")
		return respond(http.StatusOK, map[string]string{"status": "ignored", "reason": "outside night window"})
	}

	if err := db.PutPhoneLockEvent(ctx, userID, body.LockedAt); err != nil {
		log.Error().Err(err).Msg("failed to store phone-lock event")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to store event"})
	}

	log.Info().Str("user", userID).Time("locked_at", body.LockedAt).Msg("phone-lock event recorded")
	return respond(http.StatusOK, map[string]string{"status": "recorded", "locked_at": body.LockedAt.Format(time.RFC3339)})
}

func handleWhoopWebhook(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	var webhook struct {
		UserID  int    `json:"user_id"`
		ID      string `json:"id"`
		Type    string `json:"type"`
		TraceID string `json:"trace_id"`
	}

	if err := json.Unmarshal([]byte(req.Body), &webhook); err != nil {
		log.Error().Err(err).Msg("failed to parse webhook body")
		return respond(http.StatusBadRequest, map[string]string{"error": "invalid webhook body"})
	}

	log.Info().Str("type", webhook.Type).Str("id", webhook.ID).Msg("whoop webhook received")

	if err := db.PutWebhookEvent(ctx, webhook.UserID, webhook.Type, webhook.ID, webhook.TraceID); err != nil {
		log.Error().Err(err).Msg("failed to store webhook event")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to store webhook event"})
	}

	log.Info().Str("type", webhook.Type).Str("id", webhook.ID).Msg("webhook event stored")
	return respond(http.StatusOK, map[string]string{"status": "ok"})
}

func handleLast(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, err := authenticateRequest(ctx, req)
	if err != nil {
		return respond(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	sleepRec, err := db.GetLatestSyncRecord(ctx, userID, "SLEEP#")
	if err != nil {
		log.Error().Err(err).Msg("failed to get latest sleep")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to get sleep data"})
	}

	recoveryRec, err := db.GetLatestSyncRecord(ctx, userID, "RECOVERY#")
	if err != nil {
		log.Error().Err(err).Msg("failed to get latest recovery")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to get recovery data"})
	}

	phoneLock, err := db.GetLatestPhoneLockEvent(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msg("failed to get latest phone lock")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to get phone lock data"})
	}

	result := map[string]any{}

	if phoneLock != nil {
		result["phone_locked_at"] = phoneLock.LockedAt
	}

	if sleepRec != nil {
		var sleep json.RawMessage = []byte(sleepRec.Data)
		result["sleep"] = sleep
		result["sleep_synced_at"] = sleepRec.SyncedAt

		// Calculate sleep onset latency if we have both phone lock and sleep start
		if phoneLock != nil {
			var sleepData struct {
				Start time.Time `json:"start"`
			}
			if json.Unmarshal([]byte(sleepRec.Data), &sleepData) == nil && !sleepData.Start.IsZero() {
				onset := sleepData.Start.Sub(phoneLock.LockedAt)
				if onset >= 0 {
					result["sleep_onset_minutes"] = onset.Minutes()
				}
			}
		}
	}

	if recoveryRec != nil {
		var recovery json.RawMessage = []byte(recoveryRec.Data)
		result["recovery"] = recovery
		result["recovery_synced_at"] = recoveryRec.SyncedAt
	}

	if len(result) == 0 {
		return respond(http.StatusOK, map[string]string{"status": "no data synced yet"})
	}

	return respond(http.StatusOK, result)
}

func authenticateRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest) (string, error) {
	auth := req.Headers["authorization"]
	if auth == "" {
		auth = req.Headers["Authorization"]
	}

	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		return "", fmt.Errorf("missing token")
	}

	user, err := db.GetUserByBearerToken(ctx, token)
	if err != nil {
		return "", fmt.Errorf("invalid token: %w", err)
	}

	return user.PK, nil
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
		RedirectURI:  os.Getenv("WHOOP_REDIRECT_URI"),
	}, nil
}

func generateBearerToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func isNightHour(hour int) bool {
	if windowStart > windowEnd {
		return hour >= windowStart || hour < windowEnd
	}
	return hour >= windowStart && hour < windowEnd
}

func respond(statusCode int, body any) (events.APIGatewayV2HTTPResponse, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return events.APIGatewayV2HTTPResponse{
			StatusCode: http.StatusInternalServerError,
			Body:       `{"error": "internal server error"}`,
		}, nil
	}

	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "application/json"},
		Body:       string(b),
	}, nil
}

func respondHTML(statusCode int, html string) (events.APIGatewayV2HTTPResponse, error) {
	return events.APIGatewayV2HTTPResponse{
		StatusCode: statusCode,
		Headers:    map[string]string{"Content-Type": "text/html"},
		Body:       html,
	}, nil
}

func main() {
	lambda.Start(handler)
}
