package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-lambda-go/events"
	"github.com/wyattjs/whoop-sleep-onset/internal/analysis"
	"github.com/wyattjs/whoop-sleep-onset/internal/dynamo"
	"github.com/wyattjs/whoop-sleep-onset/internal/whoop"
)

type apiFake struct {
	sleeps, recoveries []dynamo.SyncRecord
	locks              []dynamo.PhoneLockEvent
}

func (f *apiFake) GetUserByBearerToken(context.Context, string) (*dynamo.User, error) {
	return &dynamo.User{PK: "USER#1"}, nil
}
func (f *apiFake) PutUser(context.Context, *dynamo.User) error                        { return nil }
func (f *apiFake) PutPhoneLockEvent(context.Context, string, time.Time) error         { return nil }
func (f *apiFake) PutWebhookEvent(context.Context, int, string, string, string) error { return nil }
func (f *apiFake) GetRecentSyncRecords(_ context.Context, _, prefix string, _ int) ([]dynamo.SyncRecord, error) {
	if prefix == "SLEEP#" {
		return f.sleeps, nil
	}
	return f.recoveries, nil
}
func (f *apiFake) GetRecentPhoneLockEvents(context.Context, string, int) ([]dynamo.PhoneLockEvent, error) {
	return f.locks, nil
}
func stored(v any) dynamo.SyncRecord {
	b, _ := json.Marshal(v)
	return dynamo.SyncRecord{Data: string(b), SyncedAt: time.Now()}
}

func TestLastDoesNotMixNightsAndLabelsStaleData(t *testing.T) {
	oldDB, oldTZ := db, userTZ
	defer func() { db, userTZ = oldDB, oldTZ }()
	userTZ = time.UTC
	start := time.Now().UTC().Add(-72 * time.Hour)
	s := whoop.SleepRecord{ID: "old", Start: start, End: start.Add(7 * time.Hour), ScoreState: "SCORED", Score: &whoop.SleepScore{}}
	db = &apiFake{sleeps: []dynamo.SyncRecord{stored(s)}, recoveries: []dynamo.SyncRecord{stored(whoop.RecoveryRecord{CycleID: 123, SleepID: "different", ScoreState: "SCORED", Score: &whoop.RecoveryScore{RecoveryScore: 99}})}, locks: []dynamo.PhoneLockEvent{{LockedAt: time.Now().Add(-time.Hour)}}}
	req := events.APIGatewayV2HTTPRequest{Headers: map[string]string{"authorization": "Bearer test"}}
	resp, err := handleLast(context.Background(), req)
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%+v %v", resp, err)
	}
	var got analysis.Night
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil {
		t.Fatal(err)
	}
	if got.Recovery != nil || got.SleepOnsetMinutes != nil || !strings.Contains(got.Status, "no completed sleep synced for today") {
		t.Fatalf("%+v", got)
	}
	db = &apiFake{}
	resp, _ = handleLast(context.Background(), req)
	if !strings.Contains(resp.Body, "no completed main sleep") {
		t.Fatal(resp.Body)
	}
}

func TestRemovedInsightsRoute(t *testing.T) {
	response, err := handler(context.Background(), events.APIGatewayV2HTTPRequest{RequestContext: events.APIGatewayV2HTTPRequestContext{HTTP: events.APIGatewayV2HTTPRequestContextHTTPDescription{Method: "GET", Path: "/insights"}}})
	if err != nil || response.StatusCode != 404 {
		t.Fatalf("removed route still active: %+v %v", response, err)
	}
}
