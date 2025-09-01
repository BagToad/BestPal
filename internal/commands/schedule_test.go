package commands

import (
	"gamerpal/internal/database"
	"os"
	"testing"
	"time"
)

func TestRouletteScheduleManagement(t *testing.T) {
	// Create a temporary database
	dbPath := "test_schedule_management.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	guildID := "123456789"

	// Test setting a schedule
	scheduleTime := time.Now().Add(2 * time.Hour).UTC()
	err = db.SetRouletteSchedule(guildID, scheduleTime)
	if err != nil {
		t.Fatalf("Failed to set schedule: %v", err)
	}

	// Test getting the schedule
	schedule, err := db.GetRouletteSchedule(guildID)
	if err != nil {
		t.Fatalf("Failed to get schedule: %v", err)
	}

	if schedule == nil {
		t.Fatal("Schedule should not be nil")
	}

	if schedule.GuildID != guildID {
		t.Fatalf("Expected guild ID %s, got %s", guildID, schedule.GuildID)
	}

	// Check that the scheduled time is close to what we set (within 1 second)
	timeDiff := schedule.ScheduledAt.Sub(scheduleTime)
	if timeDiff < -time.Second || timeDiff > time.Second {
		t.Fatalf("Scheduled time mismatch. Expected %v, got %v (diff: %v)",
			scheduleTime, schedule.ScheduledAt, timeDiff)
	}

	// Test clearing the schedule
	err = db.ClearRouletteSchedule(guildID)
	if err != nil {
		t.Fatalf("Failed to clear schedule: %v", err)
	}

	// Verify schedule was cleared
	schedule, err = db.GetRouletteSchedule(guildID)
	if err != nil {
		t.Fatalf("Failed to get schedule after clearing: %v", err)
	}

	if schedule != nil {
		t.Fatal("Schedule should be nil after clearing")
	}
}

func TestRouletteScheduleTimezone(t *testing.T) {
	// Create a temporary database
	dbPath := "test_schedule_timezone.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	guildID := "123456789"

	// Test with different timezone - ensure UTC storage
	// Simulate user input: "2024-12-25 15:30" (should be treated as UTC)
	inputTime, err := time.Parse("2006-01-02 15:04", "2024-12-25 15:30")
	if err != nil {
		t.Fatalf("Failed to parse input time: %v", err)
	}

	// Convert to UTC as the roulette_admin.go code does
	utcTime := inputTime.UTC()

	err = db.SetRouletteSchedule(guildID, utcTime)
	if err != nil {
		t.Fatalf("Failed to set schedule: %v", err)
	}

	// Retrieve and verify
	schedule, err := db.GetRouletteSchedule(guildID)
	if err != nil {
		t.Fatalf("Failed to get schedule: %v", err)
	}

	if schedule == nil {
		t.Fatal("Schedule should not be nil")
	}

	// Verify the time matches what we stored
	if !schedule.ScheduledAt.Equal(utcTime) {
		t.Fatalf("Time mismatch. Expected %v, got %v", utcTime, schedule.ScheduledAt)
	}

	// Verify it's stored in UTC
	if schedule.ScheduledAt.Location() != time.UTC {
		t.Fatalf("Schedule should be stored in UTC, got %v", schedule.ScheduledAt.Location())
	}
}
