package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var (
	colorEnabled bool

	reset   = "\033[0m"
	bold    = "\033[1m"
	dim     = "\033[2m"
	cyan    = "\033[36m"
	green   = "\033[32m"
	yellow  = "\033[33m"
	red     = "\033[31m"
	magenta = "\033[35m"
	white   = "\033[97m"
	bgCyan  = "\033[46m"
	bgGreen = "\033[42m"
	bgRed   = "\033[41m"
	bgYellow = "\033[43m"
	black   = "\033[30m"
)

func initColors() {
	colorEnabled = isatty.IsTerminal(os.Stdout.Fd()) || isatty.IsCygwinTerminal(os.Stdout.Fd())
}

func c(codes ...string) string {
	if !colorEnabled {
		return ""
	}
	return strings.Join(codes, "")
}

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
	initColors()

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

	fmt.Println()

	// Sleep Onset — the hero number
	printSleepOnset(data.SleepOnsetMin, data.PhoneLockedAt, data.Sleep)

	if data.PhoneLockedAt != nil {
		fmt.Printf("  %s%s⏱  Phone Locked%s  %s\n", c(dim), c(cyan), c(reset), data.PhoneLockedAt.Local().Format("3:04pm"))
	}

	if data.Sleep != nil {
		printSleep(data.Sleep)
	}

	if data.Recovery != nil {
		printRecovery(data.Recovery)
	}

	fmt.Println()
	return nil
}

func printSleepOnset(onset *float64, locked *time.Time, sleep *sleepData) {
	fmt.Printf("  %s╭─────────────────────────────╮%s\n", c(dim), c(reset))

	if onset != nil {
		mins := *onset
		durStr := formatDuration(time.Duration(mins * float64(time.Minute)))
		bg, fg := onsetColor(mins)
		fmt.Printf("  %s│%s  SLEEP ONSET  %s%s %s %s  %s%s│%s\n",
			c(dim), c(reset),
			c(bold, bg, fg), " ", durStr, " ", c(reset),
			c(dim), c(reset))
	} else if locked != nil && sleep != nil {
		fmt.Printf("  %s│%s  SLEEP ONSET  %s%s unable to calculate %s  %s%s│%s\n",
			c(dim), c(reset),
			c(dim), c(yellow), c(reset),
			c(dim), c(reset), c(reset))
	} else {
		fmt.Printf("  %s│%s  SLEEP ONSET  %s%s waiting for data %s     %s%s│%s\n",
			c(dim), c(reset),
			c(dim), c(yellow), c(reset),
			c(dim), c(reset), c(reset))
	}

	fmt.Printf("  %s╰─────────────────────────────╯%s\n", c(dim), c(reset))
	fmt.Println()
}

func onsetColor(mins float64) (string, string) {
	switch {
	case mins <= 15:
		return bgGreen, black
	case mins <= 30:
		return bgYellow, black
	default:
		return bgRed, white
	}
}

func printSleep(s *sleepData) {
	duration := s.End.Sub(s.Start)
	fmt.Printf("\n  %s%s SLEEP %s  %s → %s  %s(%s)%s\n",
		c(bold), c(cyan), c(reset),
		s.Start.Local().Format("3:04pm"),
		s.End.Local().Format("3:04pm"),
		c(dim), formatDuration(duration), c(reset))

	if s.Score == nil {
		fmt.Printf("  %s(score pending)%s\n", c(dim), c(reset))
		return
	}

	sc := s.Score
	fmt.Printf("  %s├%s Performance  %s\n", c(dim), c(reset), pctBar(sc.SleepPerformance))
	fmt.Printf("  %s├%s Efficiency   %s\n", c(dim), c(reset), pctBar(sc.SleepEfficiency))
	fmt.Printf("  %s├%s Consistency  %s\n", c(dim), c(reset), pctBar(sc.SleepConsistency))
	fmt.Printf("  %s├%s Respiratory  %s%.1f rpm%s\n", c(dim), c(reset), c(white), sc.RespiratoryRate, c(reset))

	st := sc.StageSummary
	fmt.Printf("  %s├%s Stages  %s%sREM%s %s  %s%sDeep%s %s  %s%sLight%s %s  %s%sAwake%s %s\n",
		c(dim), c(reset),
		c(magenta), c(bold), c(reset), formatDuration(time.Duration(st.TotalRemSleepMs)*time.Millisecond),
		c(cyan), c(bold), c(reset), formatDuration(time.Duration(st.TotalSlowWaveSleepMs)*time.Millisecond),
		c(green), c(bold), c(reset), formatDuration(time.Duration(st.TotalLightSleepMs)*time.Millisecond),
		c(red), c(bold), c(reset), formatDuration(time.Duration(st.TotalAwakeMs)*time.Millisecond))
	fmt.Printf("  %s╰%s Cycles %s%d%s  Disturbances %s%d%s\n",
		c(dim), c(reset),
		c(white), st.SleepCycleCount, c(reset),
		c(white), st.DisturbanceCount, c(reset))
}

func printRecovery(r *recoveryData) {
	fmt.Printf("\n  %s%s RECOVERY %s", c(bold), c(green), c(reset))

	if r.Score == nil {
		fmt.Printf("  %s(score pending)%s\n", c(dim), c(reset))
		return
	}

	sc := r.Score
	fmt.Printf(" %s\n", pctBar(sc.RecoveryScore))
	fmt.Printf("  %s├%s HRV          %s%.1f ms%s\n", c(dim), c(reset), c(white), sc.HRVRmssd, c(reset))
	fmt.Printf("  %s├%s Resting HR   %s%.0f bpm%s\n", c(dim), c(reset), c(white), sc.RestingHR, c(reset))
	fmt.Printf("  %s├%s SpO2         %s%.0f%%%s\n", c(dim), c(reset), c(white), sc.SPO2, c(reset))
	fmt.Printf("  %s╰%s Skin Temp    %s%.1f°C%s\n", c(dim), c(reset), c(white), sc.SkinTemp, c(reset))
}

func pctBar(pct float64) string {
	width := 16
	filled := min(int(pct/100*float64(width)), width)
	filled = max(filled, 0)

	bar := strings.Repeat("█", filled) + strings.Repeat("░", width-filled)
	clr := green
	switch {
	case pct < 50:
		clr = red
	case pct < 70:
		clr = yellow
	}
	return fmt.Sprintf("%s%s%s %s%.0f%%%s", c(clr), bar, c(reset), c(bold, white), pct, c(reset))
}

func formatDuration(d time.Duration) string {
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
