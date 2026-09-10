package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-lambda-go/events"
)

func signature(secret, message string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(message))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func header(req events.APIGatewayV2HTTPRequest, name string) string {
	for k, v := range req.Headers {
		if strings.EqualFold(k, name) {
			return v
		}
	}
	return ""
}

func requestBody(req events.APIGatewayV2HTTPRequest) ([]byte, error) {
	if req.IsBase64Encoded {
		return base64.StdEncoding.DecodeString(req.Body)
	}
	return []byte(req.Body), nil
}

func validWebhook(req events.APIGatewayV2HTTPRequest, body []byte, secret string) bool {
	timestamp := header(req, "X-WHOOP-Signature-Timestamp")
	if secret == "" || timestamp == "" {
		return false
	}
	return hmac.Equal([]byte(signature(secret, timestamp+string(body))), []byte(header(req, "X-WHOOP-Signature")))
}

// Signed state plus a browser-bound cookie survives Lambda cold starts without
// sharing an in-memory map between the start and callback invocations.
func signedState(nonce, secret string, now time.Time) string {
	payload := nonce + "." + strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10)
	return payload + "." + base64.RawURLEncoding.EncodeToString([]byte(signature(secret, "oauth:"+payload)))
}

func validState(req events.APIGatewayV2HTTPRequest, state, secret string, now time.Time) bool {
	parts := strings.Split(state, ".")
	if len(parts) != 3 || parts[0] == "" {
		return false
	}
	expiry, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || now.Unix() >= expiry || expiry > now.Add(10*time.Minute).Unix() {
		return false
	}
	expected := base64.RawURLEncoding.EncodeToString([]byte(signature(secret, "oauth:"+parts[0]+"."+parts[1])))
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return false
	}
	r := &http.Request{Header: http.Header{}}
	for _, cookie := range req.Cookies {
		r.Header.Add("Cookie", cookie)
	}
	if len(req.Cookies) == 0 {
		r.Header.Set("Cookie", header(req, "Cookie"))
	}
	cookie, err := r.Cookie("whoop_oauth_state")
	return err == nil && hmac.Equal([]byte(cookie.Value), []byte(state))
}

func stateCookie(state string, maxAge int) string {
	return (&http.Cookie{Name: "whoop_oauth_state", Value: state, Path: "/auth/whoop", Secure: true, HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: maxAge}).String()
}

func validatePhoneTime(t, now time.Time) error {
	if t.After(now.Add(time.Minute)) {
		return fmt.Errorf("locked_at cannot be in the future")
	}
	return nil
}
