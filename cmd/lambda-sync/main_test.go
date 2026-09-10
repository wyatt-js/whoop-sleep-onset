package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aws/aws-lambda-go/events"
	"github.com/wyattjs/whoop-sleep-onset/internal/dynamo"
	"github.com/wyattjs/whoop-sleep-onset/internal/whoop"
)

type fakeStore struct {
	puts    map[string]string
	deleted []string
	fail    bool
}

func (f *fakeStore) GetUserByWhoopID(context.Context, int) (*dynamo.User, error) {
	return nil, fmt.Errorf("offline")
}
func (f *fakeStore) PutUser(context.Context, *dynamo.User) error { return nil }
func (f *fakeStore) PutSyncRecord(_ context.Context, _, sk, data string) error {
	if f.fail {
		return fmt.Errorf("write failed")
	}
	f.puts[sk] = data
	return nil
}
func (f *fakeStore) DeleteSyncByID(_ context.Context, _, prefix, id string) error {
	f.deleted = append(f.deleted, prefix+id)
	return nil
}

func TestHistoricalRecoveryWebhook(t *testing.T) {
	store := &fakeStore{puts: map[string]string{}}
	old := db
	db = store
	defer func() { db = old }()
	paths := []string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		switch r.URL.Path {
		case "/activity/sleep/old-sleep":
			fmt.Fprint(w, `{"id":"old-sleep","cycle_id":123,"start":"2024-01-01T04:00:00Z","end":"2024-01-01T12:00:00Z","score_state":"SCORED","score":{}}`)
		case "/cycle/123/recovery":
			fmt.Fprint(w, `{"cycle_id":123,"sleep_id":"old-sleep","score_state":"SCORED","score":{"recovery_score":87}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c := &whoop.Client{BaseURL: srv.URL}
	if err := syncEvent(context.Background(), c, "USER#1", "recovery.updated", "old-sleep"); err != nil {
		t.Fatal(err)
	}
	if len(paths) != 2 || store.puts["SLEEP#old-sleep"] == "" || store.puts["RECOVERY#old-sleep"] == "" {
		t.Fatalf("%v %v", paths, store.puts)
	}
	store.fail = true
	if err := syncEvent(context.Background(), c, "USER#1", "sleep.updated", "old-sleep"); err == nil {
		t.Fatal("lost write error")
	}
}

func TestDeleteAndMissingObject(t *testing.T) {
	store := &fakeStore{puts: map[string]string{}}
	old := db
	db = store
	defer func() { db = old }()
	if err := syncEvent(context.Background(), nil, "USER#1", "sleep.deleted", "gone"); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 2 {
		t.Fatal(store.deleted)
	}
	srv := httptest.NewServer(http.NotFoundHandler())
	defer srv.Close()
	if err := syncEvent(context.Background(), &whoop.Client{BaseURL: srv.URL}, "USER#1", "sleep.updated", "gone"); err != nil {
		t.Fatal(err)
	}
	if len(store.deleted) != 4 {
		t.Fatal(store.deleted)
	}
}

func TestFailedStreamRecordIsRetried(t *testing.T) {
	old := db
	db = &fakeStore{}
	defer func() { db = old }()
	event := events.DynamoDBEvent{Records: []events.DynamoDBEventRecord{{EventName: "INSERT", Change: events.DynamoDBStreamRecord{NewImage: map[string]events.DynamoDBAttributeValue{
		"PK": events.NewStringAttribute("WHOOPUSER#1"), "SK": events.NewStringAttribute("WEBHOOK#sleep.updated"), "type": events.NewStringAttribute("sleep.updated"),
	}}}}}
	if err := handler(context.Background(), event); err == nil {
		t.Fatal("failed record acknowledged")
	}
}
