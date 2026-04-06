package whoop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const baseURL = "https://api.prod.whoop.com/developer/v1"

// Client wraps an access token for making authenticated WHOOP API calls.
type Client struct {
	AccessToken string
}

type PaginatedResponse[T any] struct {
	Records       []T    `json:"records"`
	NextToken     string `json:"next_token"`
}

type SleepRecord struct {
	ID             int        `json:"id"`
	UserID         int        `json:"user_id"`
	Start          time.Time  `json:"start"`
	End            time.Time  `json:"end"`
	Nap            bool       `json:"nap"`
	ScoreState     string     `json:"score_state"`
	Score          *SleepScore `json:"score"`
}

type SleepScore struct {
	StageSummary        StageSummary `json:"stage_summary"`
	SleepNeeded         Millis       `json:"sleep_needed"`
	RespiratoryRate     float64      `json:"respiratory_rate"`
	SleepPerformance    float64      `json:"sleep_performance_percentage"`
	SleepConsistency    float64      `json:"sleep_consistency_percentage"`
	SleepEfficiency     float64      `json:"sleep_efficiency_percentage"`
}

type StageSummary struct {
	TotalInBedMs         int `json:"total_in_bed_time_milli"`
	TotalAwakeMs         int `json:"total_awake_time_milli"`
	TotalNoDataMs        int `json:"total_no_data_time_milli"`
	TotalLightSleepMs    int `json:"total_light_sleep_time_milli"`
	TotalSlowWaveSleepMs int `json:"total_slow_wave_sleep_time_milli"`
	TotalRemSleepMs      int `json:"total_rem_sleep_time_milli"`
	SleepCycleCount      int `json:"sleep_cycle_count"`
	DisturbanceCount     int `json:"disturbance_count"`
}

type Millis struct {
	BaselineMs int `json:"baseline_milli"`
	NeedMs     int `json:"need_milli"`
}

type RecoveryRecord struct {
	CycleID    int            `json:"cycle_id"`
	SleepID    int            `json:"sleep_id"`
	UserID     int            `json:"user_id"`
	CreatedAt  time.Time      `json:"created_at"`
	UpdatedAt  time.Time      `json:"updated_at"`
	ScoreState string         `json:"score_state"`
	Score      *RecoveryScore `json:"score"`
}

type RecoveryScore struct {
	UserCalibrating bool    `json:"user_calibrating"`
	RecoveryScore   float64 `json:"recovery_score"`
	RestingHR       float64 `json:"resting_heart_rate"`
	HRVRmssd        float64 `json:"hrv_rmssd_milli"`
	SPO2            float64 `json:"spo2_percentage"`
	SkinTemp        float64 `json:"skin_temp_celsius"`
}

type CycleRecord struct {
	ID         int          `json:"id"`
	UserID     int          `json:"user_id"`
	Start      time.Time    `json:"start"`
	End        time.Time    `json:"end"`
	ScoreState string       `json:"score_state"`
	Score      *CycleScore  `json:"score"`
}

type CycleScore struct {
	Strain     float64 `json:"strain"`
	Kilojoule  float64 `json:"kilojoule"`
	AvgHR      float64 `json:"average_heart_rate"`
	MaxHR      float64 `json:"max_heart_rate"`
}

func (c *Client) GetSleep(ctx context.Context, start, end time.Time) ([]SleepRecord, error) {
	return fetchAll[SleepRecord](ctx, c, "/activity/sleep", start, end)
}

func (c *Client) GetRecovery(ctx context.Context, start, end time.Time) ([]RecoveryRecord, error) {
	return fetchAll[RecoveryRecord](ctx, c, "/recovery", start, end)
}

func (c *Client) GetCycles(ctx context.Context, start, end time.Time) ([]CycleRecord, error) {
	return fetchAll[CycleRecord](ctx, c, "/cycle", start, end)
}

func fetchAll[T any](ctx context.Context, c *Client, path string, start, end time.Time) ([]T, error) {
	var all []T
	nextToken := ""

	for {
		params := url.Values{
			"start": {start.Format(time.RFC3339)},
			"end":   {end.Format(time.RFC3339)},
		}
		if nextToken != "" {
			params.Set("nextToken", nextToken)
		}

		reqURL := baseURL + path + "?" + params.Encode()
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create request for %s: %w", path, err)
		}
		req.Header.Set("Authorization", "Bearer "+c.AccessToken)

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request to %s failed: %w", path, err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("%s returned status %d: %s", path, resp.StatusCode, string(body))
		}

		var page PaginatedResponse[T]
		if err := json.NewDecoder(resp.Body).Decode(&page); err != nil {
			return nil, fmt.Errorf("failed to decode %s response: %w", path, err)
		}

		all = append(all, page.Records...)

		if page.NextToken == "" {
			break
		}
		nextToken = page.NextToken
	}

	return all, nil
}

// RefreshAccessToken uses a refresh token to get a new access/refresh token pair.
func RefreshAccessToken(ctx context.Context, cfg *OAuthConfig, refreshToken string) (*TokenResponse, error) {
	data := url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {cfg.ClientID},
		"client_secret": {cfg.ClientSecret},
		"scope":         {"offline"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("refresh request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("refresh returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokens TokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tokens); err != nil {
		return nil, fmt.Errorf("failed to decode refresh response: %w", err)
	}

	return &tokens, nil
}
