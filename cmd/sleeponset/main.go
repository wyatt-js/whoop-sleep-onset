package main

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:           "sleeponset",
	Short:         "Sleep onset latency tracker powered by WHOOP",
	SilenceErrors: true,
	SilenceUsage:  true,
}

func init() {
	home, _ := os.UserHomeDir()
	cfgDir := filepath.Join(home, ".sleeponset")

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(cfgDir)

	viper.ReadInConfig()
}

func apiBaseURL() (string, error) {
	raw := strings.TrimSpace(viper.GetString("api_url"))
	if raw == "" {
		return "", fmt.Errorf("API URL not configured — run: sleeponset configure --api-url https://YOUR_API_HOST")
	}

	parsed, err := url.Parse(raw)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("API URL must be a valid HTTPS base URL")
	}

	return strings.TrimRight(raw, "/"), nil
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
