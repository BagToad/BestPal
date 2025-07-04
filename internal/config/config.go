package config

import (
	"fmt"
	"os"

	"github.com/joho/godotenv"
)

// Config holds the application configuration
type Config struct {
	BotToken        string
	IGDBClientID    string
	IGDBClientToken string
}

// Load loads the configuration from environment variables
func Load() (*Config, error) {
	// Load environment variables from .env file
	if err := godotenv.Load(); err != nil {
		// Don't return error if .env file doesn't exist
		// This allows for production deployments without .env files
	}

	cfg := &Config{
		BotToken:        os.Getenv("DISCORD_BOT_TOKEN"),
		IGDBClientID:    os.Getenv("IGDB_CLIENT_ID"),
		IGDBClientToken: os.Getenv("IGDB_CLIENT_TOKEN"),
	}

	if cfg.BotToken == "" {
		return nil, fmt.Errorf("DISCORD_BOT_TOKEN environment variable is required")
	}

	if cfg.IGDBClientID == "" {
		return nil, fmt.Errorf("IGDB_CLIENT_ID environment variable is required")
	}

	if cfg.IGDBClientToken == "" {
		return nil, fmt.Errorf("IGDB_CLIENT_TOKEN environment variable is required")
	}

	return cfg, nil
}
