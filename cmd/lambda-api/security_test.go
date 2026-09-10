package main

import (
	"encoding/base64"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func TestWebhookSignatureAndEncodedBody(t *testing.T) {
	raw := `{"type":"sleep.updated","id":"example","user_id":1}`
	req := events.APIGatewayV2HTTPRequest{Body: base64.StdEncoding.EncodeToString([]byte(raw)), IsBase64Encoded: true, Headers: map[string]string{
		"x-whoop-signature-timestamp": "1789000000000",
		"x-whoop-signature":           signature("secret", "1789000000000"+raw),
	}}
	body, err := requestBody(req)
	if err != nil || !validWebhook(req, body, "secret") {
		t.Fatalf("valid request rejected: %v", err)
	}
	if validWebhook(req, append(body, ' '), "secret") || validWebhook(req, body, "wrong") || validWebhook(req, body, "") {
		t.Fatal("invalid signature accepted")
	}
}

func TestOAuthStateSurvivesInstancesAndRequiresCookie(t *testing.T) {
	now := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	state := signedState("random-nonce", "secret", now)
	req := events.APIGatewayV2HTTPRequest{Cookies: []string{"whoop_oauth_state=" + state}}
	if !validState(req, state, "secret", now.Add(time.Minute)) {
		t.Fatal("valid state rejected")
	}
	if validState(events.APIGatewayV2HTTPRequest{}, state, "secret", now) {
		t.Fatal("missing cookie accepted")
	}
	if validState(req, state, "wrong", now) || validState(req, state, "secret", now.Add(10*time.Minute)) {
		t.Fatal("invalid state accepted")
	}
	if validState(req, state+"x", "secret", now) {
		t.Fatal("tampered state accepted")
	}
}
