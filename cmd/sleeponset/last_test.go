package main

import (
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
)

func capture(t *testing.T, fn func()) string {
	t.Helper()
	previous := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = previous }()
	fn()
	w.Close()
	b, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestSleepDisplayExcludesMissingSensorTime(t *testing.T) {
	var s sleepData
	err := json.Unmarshal([]byte(`{"id":"test","start":"2026-09-10T00:00:00Z","end":"2026-09-10T08:00:00Z","score_state":"SCORED","score":{"stage_summary":{"total_in_bed_time_milli":28800000,"total_awake_time_milli":3600000,"total_no_data_time_milli":3600000,"total_light_sleep_time_milli":14400000,"total_slow_wave_sleep_time_milli":3600000,"total_rem_sleep_time_milli":3600000}}}`), &s)
	if err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() { printSleep(&s) })
	if !strings.Contains(out, "Total asleep: 6h00m") || !strings.Contains(out, "No sensor data: 1h00m") || !strings.Contains(out, "8h00m recorded interval") || !strings.Contains(out, "unavailable") {
		t.Fatal(out)
	}
}

func TestRecoveryNumericIDAndNullMetrics(t *testing.T) {
	var r recoveryData
	if err := json.Unmarshal([]byte(`{"cycle_id":123,"sleep_id":"test","score_state":"SCORED","score":{"recovery_score":80,"spo2_percentage":null,"skin_temp_celsius":null}}`), &r); err != nil {
		t.Fatal(err)
	}
	out := capture(t, func() { printRecovery(&r) })
	if strings.Count(out, "unavailable") != 2 || strings.Contains(out, "%!") {
		t.Fatal(out)
	}
}
