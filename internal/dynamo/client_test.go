package dynamo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

func TestReadAllPagesAndUTCPhoneKeys(t *testing.T) {
	queries := 0
	puts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input map[string]any
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/x-amz-json-1.0")
		switch r.Header.Get("X-Amz-Target") {
		case "DynamoDB_20120810.Query":
			queries++
			if queries == 1 {
				fmt.Fprint(w, `{"Items":[{"PK":{"S":"USER#1"},"SK":{"S":"SLEEP#a"},"data":{"S":"{}"}}],"LastEvaluatedKey":{"PK":{"S":"USER#1"},"SK":{"S":"SLEEP#a"}}}`)
			} else {
				if input["ExclusiveStartKey"] == nil {
					t.Error("lost pagination cursor")
				}
				fmt.Fprint(w, `{"Items":[{"PK":{"S":"USER#1"},"SK":{"S":"SLEEP#b"},"data":{"S":"{}"}}]}`)
			}
		case "DynamoDB_20120810.PutItem":
			puts++
			item := input["Item"].(map[string]any)
			sk := item["SK"].(map[string]any)["S"]
			if sk != "PHONELOCK#2026-09-10T03:50:00Z" {
				t.Errorf("key=%v", sk)
			}
			fmt.Fprint(w, `{}`)
		default:
			t.Errorf("unexpected call %s", r.Header.Get("X-Amz-Target"))
		}
	}))
	defer srv.Close()
	c := &Client{db: dynamodb.New(dynamodb.Options{Region: "us-east-1", BaseEndpoint: aws.String(srv.URL), Credentials: aws.AnonymousCredentials{}, HTTPClient: srv.Client()})}
	rows, err := c.GetRecentSyncRecords(context.Background(), "USER#1", "SLEEP#", 0)
	if err != nil || len(rows) != 2 || queries != 2 {
		t.Fatalf("rows=%v queries=%d err=%v", rows, queries, err)
	}
	locked, _ := time.Parse(time.RFC3339, "2026-09-09T23:50:00-04:00")
	if err := c.PutPhoneLockEvent(context.Background(), "USER#1", locked); err != nil {
		t.Fatal(err)
	}
	if puts != 1 {
		t.Fatal(puts)
	}
}
