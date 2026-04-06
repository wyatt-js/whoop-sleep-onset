package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var rootCmd = &cobra.Command{
	Use:   "sleeponset",
	Short: "Sleep onset latency tracker powered by WHOOP",
}

func init() {
	home, _ := os.UserHomeDir()
	cfgDir := filepath.Join(home, ".sleeponset")
	os.MkdirAll(cfgDir, 0o755)

	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(cfgDir)

	viper.SetDefault("api_url", "https://ozls3538ce.execute-api.us-east-1.amazonaws.com")

	viper.ReadInConfig()
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
