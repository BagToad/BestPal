package scheduler

import (
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/pairing"
	"os"
	"testing"
	"time"

	"github.com/bwmarrin/discordgo"
)

func TestScheduler(t *testing.T) {
	// Create a temporary database in temp dir
	tmpDir := os.TempDir()
	if tmpDir == "" {
		t.Fatal("Failed to get temporary directory")
	}

	// Use a unique name for the test database
	dbPath := tmpDir + "/test_scheduler.db"
	defer os.Remove(dbPath)

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer db.Close()

	// Create mock config
	cfg := config.NewMockConfig(map[string]interface{}{
		"bot_token":         "test_token",
		"igdb_client_id":    "test_id",
		"igdb_client_token": "test_token",
		"database_path":     dbPath,
	})

	// Create mock session and pairing service
	session := &discordgo.Session{}
	pairingService := pairing.NewPairingService(session, cfg, db)

	// Create scheduler
	scheduler := NewScheduler(session, cfg, db, pairingService)

	// Test that scheduler can be created
	if scheduler == nil {
		t.Fatal("Scheduler should not be nil")
	}

	// Test starting and stopping scheduler
	scheduler.StartMinuteScheduler()

	// Let it run briefly
	time.Sleep(100 * time.Millisecond)

	scheduler.StopMinuteScheduler()

	// Test that we can get scheduled pairings (should be empty)
	scheduledPairings, err := db.GetScheduledPairings()
	if err != nil {
		t.Fatalf("Failed to get scheduled pairings: %v", err)
	}

	if len(scheduledPairings) != 0 {
		t.Fatalf("Expected 0 scheduled pairings, got %d", len(scheduledPairings))
	}

	// Test scheduling a pairing in the past (should be picked up)
	guildID := "123456789"
	pastTime := time.Now().Add(-1 * time.Minute)

	err = db.SetRouletteSchedule(guildID, pastTime)
	if err != nil {
		t.Fatalf("Failed to set schedule: %v", err)
	}

	// Check that it's in the scheduled pairings
	scheduledPairings, err = db.GetScheduledPairings()
	if err != nil {
		t.Fatalf("Failed to get scheduled pairings: %v", err)
	}

	if len(scheduledPairings) != 1 {
		t.Fatalf("Expected 1 scheduled pairing, got %d", len(scheduledPairings))
	}

	if scheduledPairings[0].GuildID != guildID {
		t.Fatalf("Expected guild ID %s, got %s", guildID, scheduledPairings[0].GuildID)
	}
}
