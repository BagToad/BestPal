package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config holds the application configuration
type Config struct {
	BotToken string
}

// Load loads the configuration from environment variables
func Load() (*Config, error) {
	// Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		// Don't return error if .env file doesn't exist
		// This allows for production deployments without .env files
	}

	cfg := &Config{
		BotToken: os.Getenv("DISCORD_BOT_TOKEN"),
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("DISCORD_BOT_TOKEN environment variable is required")
	}

	return cfg, nil
}
