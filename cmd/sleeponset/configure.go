package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var configureCmd = &cobra.Command{
	Use:   "configure",
	Short: "Save your API token",
	RunE:  runConfigure,
}

func init() {
	configureCmd.Flags().String("token", "", "Bearer token from WHOOP auth")
	configureCmd.Flags().String("api-url", "", "API Gateway URL (optional)")
	rootCmd.AddCommand(configureCmd)
}

func runConfigure(cmd *cobra.Command, args []string) error {
	token, _ := cmd.Flags().GetString("token")
	apiURL, _ := cmd.Flags().GetString("api-url")

	if token == "" && apiURL == "" {
		return fmt.Errorf("provide at least --token or --api-url")
	}

	if token != "" {
		viper.Set("token", token)
	}
	if apiURL != "" {
		viper.Set("api_url", apiURL)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	cfgPath := filepath.Join(home, ".sleeponset", "config.yaml")

	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		return err
	}
	viper.SetConfigPermissions(0o600)
	if err := viper.WriteConfigAs(cfgPath); err != nil {
		return fmt.Errorf("failed to write config: %w", err)
	}

	if err := os.Chmod(cfgPath, 0o600); err != nil {
		return err
	}
	fmt.Println("Configuration saved to ~/.sleeponset/config.yaml")
	return nil
}
