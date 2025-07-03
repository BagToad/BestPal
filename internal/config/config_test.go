package config

import (
	"os"
	"testing"
)

func TestLoad(t *testing.T) {
	// Test with missing token
	os.Unsetenv("DISCORD_BOT_TOKEN")
	_, err := Load()
	if err == nil {
		t.Error("Expected error when DISCORD_BOT_TOKEN is not set")
	}

	// Test with valid token
	os.Setenv("DISCORD_BOT_TOKEN", "test_token")
	cfg, err := Load()
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}

	if cfg.BotToken != "test_token" {
		t.Errorf("Expected BotToken to be 'test_token', got '%s'", cfg.BotToken)
	}

	// Clean up
	os.Unsetenv("DISCORD_BOT_TOKEN")
}
