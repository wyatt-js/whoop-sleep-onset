// Package analysis joins WHOOP records by identity and absolute time.
package analysis

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/wyattjs/whoop-sleep-onset/internal/dynamo"
	"github.com/wyattjs/whoop-sleep-onset/internal/whoop"
)

// MaxOnset is a conservative matching window, not a clinical threshold.
const MaxOnset = 4 * time.Hour

type Night struct {
	Sleep             whoop.SleepRecord     `json:"sleep"`
	SleepSyncedAt     time.Time             `json:"sleep_synced_at"`
	PhoneLockedAt     *time.Time            `json:"phone_locked_at,omitempty"`
	SleepOnsetMinutes *float64              `json:"sleep_onset_minutes,omitempty"`
	Recovery          *whoop.RecoveryRecord `json:"recovery,omitempty"`
	RecoverySyncedAt  *time.Time            `json:"recovery_synced_at,omitempty"`
	Status            string                `json:"status,omitempty"`
}

// Nights retains the latest stored version of each sleep, excludes naps and
// unfinished sleeps, and never pairs records by calendar date or list position.
func Nights(sleeps, recoveries []dynamo.SyncRecord, locks []dynamo.PhoneLockEvent, now time.Time) ([]Night, error) {
	byID := map[string]Night{}
	for _, row := range sleeps {
		var s whoop.SleepRecord
		if err := json.Unmarshal([]byte(row.Data), &s); err != nil {
			return nil, fmt.Errorf("decode stored sleep: %w", err)
		}
		if s.ID == "" {
			return nil, fmt.Errorf("stored sleep has no ID")
		}
		if old, ok := byID[s.ID]; ok && !row.SyncedAt.After(old.SleepSyncedAt) {
			continue
		}
		byID[s.ID] = Night{Sleep: s, SleepSyncedAt: row.SyncedAt}
	}
	recoveryBySleep := map[string]struct {
		record whoop.RecoveryRecord
		synced time.Time
	}{}
	for _, row := range recoveries {
		var r whoop.RecoveryRecord
		if err := json.Unmarshal([]byte(row.Data), &r); err != nil {
			return nil, fmt.Errorf("decode stored recovery: %w", err)
		}
		if old, ok := recoveryBySleep[r.SleepID]; !ok || row.SyncedAt.After(old.synced) {
			recoveryBySleep[r.SleepID] = struct {
				record whoop.RecoveryRecord
				synced time.Time
			}{r, row.SyncedAt}
		}
	}
	var nights []Night
	for _, n := range byID {
		s := n.Sleep
		if s.Nap || s.Start.IsZero() || !s.End.After(s.Start) || s.End.After(now) {
			continue
		}
		if s.ScoreState != "SCORED" {
			n.Sleep.Score = nil
		}
		if r, ok := recoveryBySleep[s.ID]; ok && r.record.ScoreState == "SCORED" && r.record.Score != nil {
			n.Recovery = &r.record
			n.RecoverySyncedAt = &r.synced
		}
		nights = append(nights, n)
	}
	sort.Slice(nights, func(i, j int) bool { return nights[i].Sleep.Start.After(nights[j].Sleep.Start) })
	used := map[int64]bool{}
	for i := range nights {
		n := &nights[i]
		if n.Sleep.ScoreState != "SCORED" || n.Sleep.Score == nil {
			n.Status = "sleep score unavailable: " + n.Sleep.ScoreState
			continue
		}
		for _, lock := range locks {
			t := lock.LockedAt
			delta := n.Sleep.Start.Sub(t)
			if t.IsZero() || delta < 0 || delta > MaxOnset || used[t.UnixNano()] {
				continue
			}
			// A lock during another sleep is not a new bedtime intent.
			overlaps := false
			for _, other := range byID {
				if !t.Before(other.Sleep.Start) && t.Before(other.Sleep.End) && other.Sleep.ID != n.Sleep.ID {
					overlaps = true
					break
				}
			}
			if overlaps {
				continue
			}
			if n.PhoneLockedAt == nil || t.After(*n.PhoneLockedAt) {
				n.PhoneLockedAt = &t
			}
		}
		if n.PhoneLockedAt == nil {
			n.Status = "no phone event within four hours before this sleep"
			continue
		}
		used[n.PhoneLockedAt.UnixNano()] = true
		mins := n.Sleep.Start.Sub(*n.PhoneLockedAt).Minutes()
		n.SleepOnsetMinutes = &mins
	}
	return nights, nil
}

// Recent selects completed sleeps by local wake date, including today. Calendar
// arithmetic preserves seven dates even across daylight-saving transitions.
func Recent(nights []Night, now time.Time, loc *time.Location, days int) []Night {
	if days <= 0 {
		return nil
	}
	local := now.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -days+1)
	var out []Night
	for _, n := range nights {
		if !n.Sleep.End.Before(start) && !n.Sleep.End.After(now) {
			out = append(out, n)
		}
	}
	return out
}
