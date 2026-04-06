package main

import (
	"fmt"
	"os/exec"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate with WHOOP via browser",
	RunE:  runAuth,
}

func init() {
	rootCmd.AddCommand(authCmd)
}

func runAuth(cmd *cobra.Command, args []string) error {
	apiURL := viper.GetString("api_url")
	authURL := apiURL + "/auth/whoop/start"
	fmt.Println("Opening browser to authenticate with WHOOP...")
	return exec.Command("open", authURL).Start()
}
