package analysis

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/wyattjs/whoop-sleep-onset/internal/dynamo"
	"github.com/wyattjs/whoop-sleep-onset/internal/whoop"
)

func at(s string) time.Time {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return t
}
func row(v any, synced time.Time) dynamo.SyncRecord {
	b, _ := json.Marshal(v)
	return dynamo.SyncRecord{Data: string(b), SyncedAt: synced}
}
func sleep(id, start string) whoop.SleepRecord {
	t := at(start)
	return whoop.SleepRecord{ID: id, Start: t, End: t.Add(7 * time.Hour), ScoreState: "SCORED", Score: &whoop.SleepScore{}}
}

func TestMatchingBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, start, lock string
		want              *float64
	}{
		{"midnight", "2026-09-10T00:10:00-04:00", "2026-09-09T23:50:00-04:00", ptr(20)},
		{"spring DST", "2026-03-08T03:10:00-04:00", "2026-03-08T01:50:00-05:00", ptr(20)},
		{"fall DST", "2026-11-01T01:10:00-05:00", "2026-11-01T01:50:00-04:00", ptr(20)},
		{"different offsets", "2026-09-10T04:10:00Z", "2026-09-09T23:50:00-04:00", ptr(20)},
		{"missing night", "2026-09-10T04:10:00Z", "2026-09-09T03:50:00Z", nil},
		{"after start", "2026-09-10T04:10:00Z", "2026-09-10T04:11:00Z", nil},
		{"exact start", "2026-09-10T04:10:00Z", "2026-09-10T04:10:00Z", ptr(0)},
		{"four hours", "2026-09-10T04:10:00Z", "2026-09-10T00:10:00Z", ptr(240)},
		{"over four hours", "2026-09-10T04:10:00Z", "2026-09-10T00:09:59Z", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := sleep("a", tc.start)
			got, err := Nights([]dynamo.SyncRecord{row(s, s.End)}, nil, []dynamo.PhoneLockEvent{{LockedAt: at(tc.lock)}}, s.End)
			if err != nil || len(got) != 1 {
				t.Fatalf("%v %v", got, err)
			}
			onset := got[0].SleepOnsetMinutes
			if tc.want == nil {
				if onset != nil {
					t.Fatalf("unexpected onset %v", *onset)
				}
			} else if onset == nil || *onset != *tc.want {
				t.Fatalf("got %v want %v", onset, *tc.want)
			}
		})
	}
}
func ptr(v float64) *float64 { return &v }

func TestMissingNightsNapsAndRecoveryIdentity(t *testing.T) {
	now := at("2026-09-10T20:00:00Z")
	first := sleep("first", "2026-09-08T04:00:00Z")
	latest := sleep("latest", "2026-09-10T04:00:00Z")
	nap := sleep("nap", "2026-09-10T12:00:00Z")
	nap.Nap = true
	rows := []dynamo.SyncRecord{row(nap, now), row(first, now), row(latest, now)}
	locks := []dynamo.PhoneLockEvent{{LockedAt: at("2026-09-10T03:20:00Z")}, {LockedAt: at("2026-09-09T03:50:00Z")}, {LockedAt: at("2026-09-10T03:50:00Z")}}
	rec := whoop.RecoveryRecord{CycleID: 123, SleepID: "first", ScoreState: "SCORED", Score: &whoop.RecoveryScore{RecoveryScore: 80}}
	got, err := Nights(rows, []dynamo.SyncRecord{row(rec, now)}, locks, now)
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if got[0].Sleep.ID != "latest" || got[0].Recovery != nil || got[0].SleepOnsetMinutes == nil || *got[0].SleepOnsetMinutes != 10 {
		t.Fatalf("bad latest: %+v", got[0])
	}
	if got[1].SleepOnsetMinutes != nil || got[1].Recovery == nil || got[1].Recovery.SleepID != "first" {
		t.Fatalf("bad first: %+v", got[1])
	}
}

func TestLatestRevisionAndPendingScore(t *testing.T) {
	now := at("2026-09-10T20:00:00Z")
	old := sleep("same", "2026-09-10T04:00:00Z")
	edited := old
	edited.Start = old.Start.Add(time.Hour)
	edited.ScoreState = "PENDING_SCORE"
	got, err := Nights([]dynamo.SyncRecord{row(old, now.Add(-time.Hour)), row(edited, now)}, nil, []dynamo.PhoneLockEvent{{LockedAt: old.Start}}, now)
	if err != nil || len(got) != 1 || got[0].SleepOnsetMinutes != nil || got[0].Sleep.Score != nil || !got[0].Sleep.Start.Equal(edited.Start) {
		t.Fatalf("%+v %v", got, err)
	}
	edited.Nap = true
	got, err = Nights([]dynamo.SyncRecord{row(old, now.Add(-time.Hour)), row(edited, now)}, nil, nil, now)
	if err != nil || len(got) != 0 {
		t.Fatalf("edited nap leaked: %+v %v", got, err)
	}
}

func TestCalendarWindowDST(t *testing.T) {
	loc, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	now := at("2026-03-10T12:00:00-04:00")
	start := at("2026-03-04T00:00:00-05:00")
	nights := []Night{{Sleep: whoop.SleepRecord{End: start.Add(-time.Nanosecond)}}, {Sleep: whoop.SleepRecord{End: start}}, {Sleep: whoop.SleepRecord{End: now.Add(time.Second)}}}
	got := Recent(nights, now, loc, 7)
	if len(got) != 1 || !got[0].Sleep.End.Equal(start) {
		t.Fatalf("%+v", got)
	}
}

func TestInvalidAndUnfinishedData(t *testing.T) {
	now := at("2026-09-10T06:00:00Z")
	s := sleep("ongoing", "2026-09-10T04:00:00Z")
	got, err := Nights([]dynamo.SyncRecord{row(s, now)}, nil, nil, now)
	if err != nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := Nights([]dynamo.SyncRecord{{Data: "broken"}}, nil, nil, now); err == nil {
		t.Fatal("malformed JSON silently ignored")
	}
}
