package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var insightsCmd = &cobra.Command{
	Use:   "insights",
	Short: "Get AI-powered analysis of your recent sleep data",
	RunE:  runInsights,
}

func init() {
	rootCmd.AddCommand(insightsCmd)
}

type insightsResponse struct {
	Insights string `json:"insights"`
	Status   string `json:"status"`
}

func runInsights(cmd *cobra.Command, args []string) error {
	initColors()

	token := viper.GetString("token")
	if token == "" {
		return fmt.Errorf("not authenticated — run: sleeponset configure --token <token>")
	}

	apiURL := viper.GetString("api_url")

	fmt.Printf("\n  %s%s Analyzing your sleep data...%s\n\n", c(dim), c(cyan), c(reset))

	req, err := http.NewRequest(http.MethodGet, apiURL+"/insights", nil)
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

	var data insightsResponse
	if err := json.Unmarshal(body, &data); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if data.Status != "" {
		fmt.Printf("  %s%s%s\n\n", c(dim), data.Status, c(reset))
		return nil
	}

	printInsights(data.Insights)
	return nil
}

func printInsights(text string) {
	fmt.Printf("  %s╭─────────────────────────────────╮%s\n", c(dim), c(reset))
	fmt.Printf("  %s│%s  %s%sSLEEP INSIGHTS%s              %s│%s\n",
		c(dim), c(reset), c(bold), c(cyan), c(reset), c(dim), c(reset))
	fmt.Printf("  %s╰─────────────────────────────────╯%s\n\n", c(dim), c(reset))

	lines := strings.Split(text, "\n")
	for _, line := range lines {
		if line == "" {
			fmt.Println()
			continue
		}

		// Style markdown headers
		if strings.HasPrefix(line, "###") {
			heading := strings.TrimPrefix(line, "### ")
			heading = strings.TrimPrefix(heading, "###")
			fmt.Printf("  %s%s%s%s\n", c(bold), c(magenta), strings.TrimSpace(heading), c(reset))
			continue
		}
		if strings.HasPrefix(line, "##") {
			heading := strings.TrimPrefix(line, "## ")
			heading = strings.TrimPrefix(heading, "##")
			fmt.Printf("  %s%s%s%s\n", c(bold), c(cyan), strings.TrimSpace(heading), c(reset))
			continue
		}
		if strings.HasPrefix(line, "#") {
			heading := strings.TrimPrefix(line, "# ")
			heading = strings.TrimPrefix(heading, "#")
			fmt.Printf("  %s%s%s%s\n", c(bold), c(green), strings.TrimSpace(heading), c(reset))
			continue
		}

		// Style numbered items
		if len(line) > 2 && line[0] >= '1' && line[0] <= '9' && line[1] == '.' {
			fmt.Printf("  %s%s%s%s%s\n", c(bold), c(yellow), string(line[0:2]), c(reset), line[2:])
			continue
		}

		// Style bullet points
		if strings.HasPrefix(line, "- ") || strings.HasPrefix(line, "* ") {
			fmt.Printf("  %s%s•%s %s\n", c(bold), c(cyan), c(reset), line[2:])
			continue
		}

		// Bold text inline
		formatted := line
		for {
			start := strings.Index(formatted, "**")
			if start == -1 {
				break
			}
			end := strings.Index(formatted[start+2:], "**")
			if end == -1 {
				break
			}
			end += start + 2
			boldText := formatted[start+2 : end]
			formatted = formatted[:start] + c(bold, white) + boldText + c(reset) + formatted[end+2:]
		}

		fmt.Printf("  %s\n", formatted)
	}

	fmt.Println()
}
