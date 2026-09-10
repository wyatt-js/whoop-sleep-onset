package whoop

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCollectionsAndPagination(t *testing.T) {
	calls := 0
	start := time.Date(2026, 9, 9, 23, 0, 0, 123000000, time.FixedZone("EDT", -4*3600))
	end := start.Add(24 * time.Hour)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test" {
			t.Error("missing token")
		}
		q := r.URL.Query()
		if q.Get("start") != start.UTC().Format(time.RFC3339Nano) || q.Get("end") != end.UTC().Format(time.RFC3339Nano) || q.Get("limit") != "25" {
			t.Errorf("bad bounds: %v", q)
		}
		switch r.URL.Path {
		case "/activity/sleep":
			if q.Get("nextToken") == "" {
				fmt.Fprint(w, `{"records":[{"id":"sleep-a","cycle_id":93845,"timezone_offset":"-04:00","score_state":"PENDING_SCORE","score":null}],"next_token":"a+b/=token"}`)
			} else {
				if q.Get("nextToken") != "a+b/=token" {
					t.Error("token not preserved")
				}
				fmt.Fprint(w, `{"records":[{"id":"sleep-b","cycle_id":93846}],"next_token":null}`)
			}
		case "/recovery":
			fmt.Fprint(w, `{"records":[{"cycle_id":93845,"sleep_id":"sleep-a","score_state":"PENDING_SCORE","score":null}]}`)
		case "/cycle":
			fmt.Fprint(w, `{"records":[{"id":93845,"end":null}]}`)
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	}))
	defer srv.Close()
	c := &Client{AccessToken: "test", BaseURL: srv.URL, HTTPClient: srv.Client()}
	sleeps, err := c.GetSleep(context.Background(), start, end)
	if err != nil || len(sleeps) != 2 || sleeps[0].CycleID != 93845 || sleeps[0].Score != nil {
		t.Fatalf("%v %v", sleeps, err)
	}
	rec, err := c.GetRecovery(context.Background(), start, end)
	if err != nil || len(rec) != 1 || rec[0].CycleID != 93845 {
		t.Fatalf("%v %v", rec, err)
	}
	cycles, err := c.GetCycles(context.Background(), start, end)
	if err != nil || len(cycles) != 1 || cycles[0].ID != 93845 || !cycles[0].End.IsZero() {
		t.Fatalf("%v %v", cycles, err)
	}
	if calls != 4 {
		t.Fatalf("calls=%d", calls)
	}
}

func TestClientErrors(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"rate limit", 429, `{"error":"slow down"}`}, {"bad JSON", 200, `{`}, {"repeated token", 200, `{"records":[],"next_token":"same"}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer srv.Close()
			c := &Client{BaseURL: srv.URL}
			now := time.Now()
			_, err := c.GetSleep(context.Background(), now.Add(-time.Hour), now)
			if err == nil {
				t.Fatal("expected error")
			}
			if tc.status == 429 {
				var he *HTTPError
				if !errors.As(err, &he) || he.StatusCode != 429 {
					t.Fatal(err)
				}
			}
		})
	}
	c := &Client{}
	if _, err := c.GetSleep(context.Background(), time.Now(), time.Time{}); err == nil {
		t.Fatal("invalid bounds accepted")
	}
}
