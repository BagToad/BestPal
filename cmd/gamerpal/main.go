package main

import (
	"os"

	"gamerpal/internal/bot"
	"gamerpal/internal/config"

	"github.com/charmbracelet/log"
)

func main() {
	// Load configuration
	cfg, err := config.NewConfig()
	if err != nil {
		// Use a fallback logger if config fails to load
		logger := log.New(os.Stderr)
		logger.Fatal("Failed to load configuration:", err)
		return
	}

	// Create and start bot
	bestPalBot, err := bot.New(cfg)
	if err != nil {
		cfg.Logger.Fatal("Failed to create bot:", err)
	}

	if err := bestPalBot.Start(); err != nil {
		cfg.Logger.Fatal("Failed to start bot:", err)
	}
}
