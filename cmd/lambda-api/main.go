package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"strings"
	"time"
	_ "time/tzdata"

	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"

	"github.com/wyattjs/whoop-sleep-onset/internal/analysis"
	"github.com/wyattjs/whoop-sleep-onset/internal/dynamo"
	"github.com/wyattjs/whoop-sleep-onset/internal/whoop"
)

var (
	db       apiStore
	oauthCfg *whoop.OAuthConfig

	userTZ *time.Location
)

type apiStore interface {
	GetUserByBearerToken(context.Context, string) (*dynamo.User, error)
	PutUser(context.Context, *dynamo.User) error
	PutPhoneLockEvent(context.Context, string, time.Time) error
	PutWebhookEvent(context.Context, int, string, string, string) error
	GetRecentSyncRecords(context.Context, string, string, int) ([]dynamo.SyncRecord, error)
	GetRecentPhoneLockEvents(context.Context, string, int) ([]dynamo.PhoneLockEvent, error)
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

	state = signedState(state, oauthCfg.ClientSecret, time.Now())

	authURL := whoop.BuildAuthURL(oauthCfg, state)

	log.Info().Msg("redirecting to WHOOP OAuth")

	return events.APIGatewayV2HTTPResponse{
		StatusCode: http.StatusFound,
		Cookies:    []string{stateCookie(state, 600)},
		Headers: map[string]string{
			"Location": authURL,
		},
	}, nil
}

func handleAuthCallback(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	if errParam := req.QueryStringParameters["error"]; errParam != "" {
		log.Error().Str("error", errParam).Msg("oauth error from WHOOP")
		return respondHTML(http.StatusBadRequest, fmt.Sprintf(
			"<h1>Authentication Failed</h1><p>WHOOP returned an error: %s</p>", html.EscapeString(errParam),
		))
	}

	state := req.QueryStringParameters["state"]
	if !validState(req, state, oauthCfg.ClientSecret, time.Now()) {
		return respondHTML(http.StatusBadRequest, "<h1>Authentication Failed</h1><p>Invalid or expired state.</p>")
	}

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
	`, html.EscapeString(profile.FirstName), bearerToken, bearerToken)

	resp, err := respondHTML(http.StatusOK, html)
	resp.Cookies = []string{stateCookie("", -1)}
	resp.Headers["Cache-Control"] = "no-store"
	return resp, err
}

func handlePhoneLock(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	userID, err := authenticateRequest(ctx, req)
	if err != nil {
		return respond(http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
	}

	var body struct {
		LockedAt time.Time `json:"locked_at"`
	}

	raw, err := requestBody(req)
	if err != nil {
		return respond(http.StatusBadRequest, map[string]string{"error": "invalid body encoding"})
	}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &body); err != nil {
			log.Error().Err(err).Msg("failed to parse phone-lock body")
			return respond(http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		}
	}

	if body.LockedAt.IsZero() {
		body.LockedAt = time.Now().UTC()
	}

	if err := validatePhoneTime(body.LockedAt, time.Now()); err != nil {
		return respond(http.StatusBadRequest, map[string]string{"error": err.Error()})
	}
	body.LockedAt = body.LockedAt.UTC()
	if err := db.PutPhoneLockEvent(ctx, userID, body.LockedAt); err != nil {
		log.Error().Err(err).Msg("failed to store phone-lock event")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to store event"})
	}

	log.Info().Str("user", userID).Time("locked_at", body.LockedAt).Msg("phone-lock event recorded")
	return respond(http.StatusOK, map[string]string{"status": "recorded", "locked_at": body.LockedAt.Format(time.RFC3339)})
}

func handleWhoopWebhook(ctx context.Context, req events.APIGatewayV2HTTPRequest) (events.APIGatewayV2HTTPResponse, error) {
	raw, err := requestBody(req)
	if err != nil {
		return respond(http.StatusBadRequest, map[string]string{"error": "invalid body encoding"})
	}
	if !validWebhook(req, raw, oauthCfg.ClientSecret) {
		return respond(http.StatusUnauthorized, map[string]string{"error": "invalid webhook signature"})
	}
	var webhook struct {
		UserID  int    `json:"user_id"`
		ID      string `json:"id"`
		Type    string `json:"type"`
		TraceID string `json:"trace_id"`
	}

	if err := json.Unmarshal(raw, &webhook); err != nil {
		log.Error().Err(err).Msg("failed to parse webhook body")
		return respond(http.StatusBadRequest, map[string]string{"error": "invalid webhook body"})
	}

	if webhook.UserID <= 0 || webhook.ID == "" || webhook.TraceID == "" {
		return respond(http.StatusBadRequest, map[string]string{"error": "missing webhook fields"})
	}
	switch webhook.Type {
	case "sleep.updated", "sleep.deleted", "recovery.updated", "recovery.deleted":
	default:
		return respond(http.StatusOK, map[string]string{"status": "ignored"})
	}

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

	nights, err := loadNights(ctx, userID)
	if err != nil {
		log.Error().Err(err).Msg("failed to load nights")
		return respond(http.StatusInternalServerError, map[string]string{"error": "failed to load sleep data"})
	}
	if len(nights) == 0 {
		return respond(http.StatusOK, map[string]string{"status": "no completed main sleep synced yet"})
	}
	latest := nights[0]
	if len(analysis.Recent(nights[:1], time.Now(), userTZ, 1)) == 0 {
		latest.Status = "showing latest recorded sleep; no completed sleep synced for today. " + latest.Status
	}
	return respond(http.StatusOK, latest)
}

func loadNights(ctx context.Context, userID string) ([]analysis.Night, error) {
	sleeps, err := db.GetRecentSyncRecords(ctx, userID, "SLEEP#", 0)
	if err != nil {
		return nil, err
	}
	recoveries, err := db.GetRecentSyncRecords(ctx, userID, "RECOVERY#", 0)
	if err != nil {
		return nil, err
	}
	locks, err := db.GetRecentPhoneLockEvents(ctx, userID, 0)
	if err != nil {
		return nil, err
	}
	return analysis.Nights(sleeps, recoveries, locks, time.Now())
}

func authenticateRequest(ctx context.Context, req events.APIGatewayV2HTTPRequest) (string, error) {
	auth := req.Headers["authorization"]
	if auth == "" {
		auth = req.Headers["Authorization"]
	}

	scheme, token, ok := strings.Cut(auth, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
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

func generateBearerToken() (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
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
	initialize()
	lambda.Start(handler)
}
