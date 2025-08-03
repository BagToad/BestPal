package main

import (
	"log"

	"gamerpal/internal/bot"
	"gamerpal/internal/config"
)

func main() {
	// Load configuration
	cfg, err := config.NewConfig()
	if err != nil {
		log.Fatal("Failed to load configuration:", err)
	}

	// Create and start bot
	bestPalBot, err := bot.New(cfg)
	if err != nil {
		log.Fatal("Failed to create bot:", err)
	}

	if err := bestPalBot.Start(); err != nil {
		log.Fatal("Failed to start bot:", err)
	}
}
