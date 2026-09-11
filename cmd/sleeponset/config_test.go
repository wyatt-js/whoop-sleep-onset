package main

import (
	"testing"

	"github.com/spf13/viper"
)

func TestAPIBaseURL(t *testing.T) {
	t.Cleanup(viper.Reset)

	for _, invalid := range []string{"", "example.com", "http://example.com", "https://", "https://user@example.com", "https://example.com?token=x"} {
		viper.Set("api_url", invalid)
		if _, err := apiBaseURL(); err == nil {
			t.Fatalf("accepted invalid API URL %q", invalid)
		}
	}

	viper.Set("api_url", " https://example.com/prod/ ")
	got, err := apiBaseURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "https://example.com/prod" {
		t.Fatalf("got %q", got)
	}
}

func TestBrowserCommands(t *testing.T) {
	for _, test := range []struct {
		goos string
		want string
	}{
		{goos: "darwin", want: "open"},
		{goos: "linux", want: "xdg-open"},
		{goos: "windows", want: "rundll32"},
	} {
		name, args, err := browserCommand(test.goos, "https://example.com")
		if err != nil {
			t.Fatal(err)
		}
		if name != test.want || args[len(args)-1] != "https://example.com" {
			t.Fatalf("%s: %s %v", test.goos, name, args)
		}
	}

	if _, _, err := browserCommand("plan9", "https://example.com"); err == nil {
		t.Fatal("unsupported platform accepted")
	}
}
