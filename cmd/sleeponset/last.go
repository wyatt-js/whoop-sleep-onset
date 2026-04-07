package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var lastCmd = &cobra.Command{
	Use:   "last",
	Short: "Show last night's sleep and recovery data",
	RunE:  runLast,
}

func init() {
	rootCmd.AddCommand(lastCmd)
}

type lastResponse struct {
	PhoneLockedAt    *time.Time    `json:"phone_locked_at"`
	Sleep            *sleepData    `json:"sleep"`
	SleepSyncedAt    *time.Time    `json:"sleep_synced_at"`
	SleepOnsetMin    *float64      `json:"sleep_onset_minutes"`
	Recovery         *recoveryData `json:"recovery"`
	RecoverySyncedAt *time.Time    `json:"recovery_synced_at"`
	Status           string        `json:"status"`
}

type sleepData struct {
	ID    string    `json:"id"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Nap   bool      `json:"nap"`
	Score *sleepScore `json:"score"`
}

type sleepScore struct {
	StageSummary     stageSummary `json:"stage_summary"`
	RespiratoryRate  float64      `json:"respiratory_rate"`
	SleepPerformance float64      `json:"sleep_performance_percentage"`
	SleepConsistency float64      `json:"sleep_consistency_percentage"`
	SleepEfficiency  float64      `json:"sleep_efficiency_percentage"`
}

type stageSummary struct {
	TotalInBedMs         int `json:"total_in_bed_time_milli"`
	TotalAwakeMs         int `json:"total_awake_time_milli"`
	TotalLightSleepMs    int `json:"total_light_sleep_time_milli"`
	TotalSlowWaveSleepMs int `json:"total_slow_wave_sleep_time_milli"`
	TotalRemSleepMs      int `json:"total_rem_sleep_time_milli"`
	SleepCycleCount      int `json:"sleep_cycle_count"`
	DisturbanceCount     int `json:"disturbance_count"`
}

type recoveryData struct {
	CycleID    string         `json:"cycle_id"`
	SleepID    string         `json:"sleep_id"`
	ScoreState string         `json:"score_state"`
	Score      *recoveryScore `json:"score"`
}

type recoveryScore struct {
	RecoveryScore float64 `json:"recovery_score"`
	RestingHR     float64 `json:"resting_heart_rate"`
	HRVRmssd      float64 `json:"hrv_rmssd_milli"`
	SPO2          float64 `json:"spo2_percentage"`
	SkinTemp      float64 `json:"skin_temp_celsius"`
}

func runLast(cmd *cobra.Command, args []string) error {
	token := viper.GetString("token")
	if token == "" {
		return fmt.Errorf("not authenticated — run: sleeponset configure --token <token>")
	}

	apiURL := viper.GetString("api_url")
	req, err := http.NewRequest(http.MethodGet, apiURL+"/last", nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var data lastResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if data.Status != "" {
		fmt.Println(data.Status)
		return nil
	}

	if data.PhoneLockedAt != nil {
		fmt.Printf("Phone Locked: %s\n", data.PhoneLockedAt.Local().Format("3:04pm"))
	}

	if data.Sleep != nil {
		printSleep(data.Sleep)
	}

	if data.SleepOnsetMin != nil {
		fmt.Printf("\nSleep Onset:  %s\n", formatDuration(time.Duration(*data.SleepOnsetMin*float64(time.Minute))))
	} else if data.PhoneLockedAt != nil && data.Sleep != nil {
		fmt.Printf("\nSleep Onset:  (unable to calculate)\n")
	}

	if data.Recovery != nil {
		fmt.Println()
		printRecovery(data.Recovery)
	}

	return nil
}

func printSleep(s *sleepData) {
	duration := s.End.Sub(s.Start)
	fmt.Printf("Sleep  %s → %s (%s)\n",
		s.Start.Local().Format("3:04pm"),
		s.End.Local().Format("3:04pm"),
		formatDuration(duration))

	if s.Score == nil {
		fmt.Println("  (score pending)")
		return
	}

	sc := s.Score
	fmt.Printf("  Performance:  %.0f%%\n", sc.SleepPerformance)
	fmt.Printf("  Efficiency:   %.0f%%\n", sc.SleepEfficiency)
	fmt.Printf("  Consistency:  %.0f%%\n", sc.SleepConsistency)
	fmt.Printf("  Respiratory:  %.1f rpm\n", sc.RespiratoryRate)

	st := sc.StageSummary
	fmt.Printf("  Stages:       REM %s · Deep %s · Light %s · Awake %s\n",
		formatDuration(time.Duration(st.TotalRemSleepMs)*time.Millisecond),
		formatDuration(time.Duration(st.TotalSlowWaveSleepMs)*time.Millisecond),
		formatDuration(time.Duration(st.TotalLightSleepMs)*time.Millisecond),
		formatDuration(time.Duration(st.TotalAwakeMs)*time.Millisecond))
	fmt.Printf("  Cycles: %d  Disturbances: %d\n", st.SleepCycleCount, st.DisturbanceCount)
}

func printRecovery(r *recoveryData) {
	fmt.Println("Recovery")
	if r.Score == nil {
		fmt.Println("  (score pending)")
		return
	}

	sc := r.Score
	fmt.Printf("  Score:     %.0f%%\n", sc.RecoveryScore)
	fmt.Printf("  HRV:       %.1f ms\n", sc.HRVRmssd)
	fmt.Printf("  Resting HR: %.0f bpm\n", sc.RestingHR)
	fmt.Printf("  SpO2:      %.0f%%\n", sc.SPO2)
	fmt.Printf("  Skin Temp: %.1f°C\n", sc.SkinTemp)
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
