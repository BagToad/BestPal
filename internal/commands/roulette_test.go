package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/pairing"
	"os"
	"testing"
	"time"
)

func TestRouletteDatabase(t *testing.T) {
	// Create a temporary database
	dbPath := "test_roulette.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	guildID := "123456789"
	userID1 := "user1"
	userID2 := "user2"

	// Test signup
	err = db.AddRouletteSignup(userID1, guildID)
	if err != nil {
		t.Fatalf("Failed to add signup: %v", err)
	}

	err = db.AddRouletteSignup(userID2, guildID)
	if err != nil {
		t.Fatalf("Failed to add second signup: %v", err)
	}

	// Test get signups
	signups, err := db.GetRouletteSignups(guildID)
	if err != nil {
		t.Fatalf("Failed to get signups: %v", err)
	}

	if len(signups) != 2 {
		t.Fatalf("Expected 2 signups, got %d", len(signups))
	}

	// Test is signed up
	isSignedUp, err := db.IsUserSignedUp(userID1, guildID)
	if err != nil {
		t.Fatalf("Failed to check signup: %v", err)
	}

	if !isSignedUp {
		t.Fatal("User should be signed up")
	}

	// Test add games
	err = db.AddRouletteGame(userID1, guildID, "Test Game 1", 123)
	if err != nil {
		t.Fatalf("Failed to add game: %v", err)
	}

	err = db.AddRouletteGame(userID1, guildID, "Test Game 2", 456)
	if err != nil {
		t.Fatalf("Failed to add second game: %v", err)
	}

	// Test get games
	games, err := db.GetRouletteGames(userID1, guildID)
	if err != nil {
		t.Fatalf("Failed to get games: %v", err)
	}

	if len(games) != 2 {
		t.Fatalf("Expected 2 games, got %d", len(games))
	}

	// Test schedule
	scheduleTime := time.Now().Add(time.Hour)
	err = db.SetRouletteSchedule(guildID, scheduleTime)
	if err != nil {
		t.Fatalf("Failed to set schedule: %v", err)
	}

	schedule, err := db.GetRouletteSchedule(guildID)
	if err != nil {
		t.Fatalf("Failed to get schedule: %v", err)
	}

	if schedule == nil {
		t.Fatal("Schedule should not be nil")
	}

	// Test remove signup
	err = db.RemoveRouletteSignup(userID1, guildID)
	if err != nil {
		t.Fatalf("Failed to remove signup: %v", err)
	}

	isSignedUp, err = db.IsUserSignedUp(userID1, guildID)
	if err != nil {
		t.Fatalf("Failed to check signup after removal: %v", err)
	}

	if isSignedUp {
		t.Fatal("User should not be signed up after removal")
	}
}

func TestPairingAlgorithm(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{
		"bot_token":         "test_token",
		"igdb_client_id":    "test_id",
		"igdb_client_token": "test_token",
	})

	// Create a temporary database
	dbPath := "test_pairing.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create pairing service (session can be nil for this test)
	pairingService := pairing.NewPairingService(nil, cfg, db)

	// Create test users - ensure they can form a group with the new algorithm
	users := []pairing.User{
		{
			UserID:  "user1",
			Games:   []string{"Game A", "Game B"},
			Regions: []string{"NA"},
			Paired:  false,
		},
		{
			UserID:  "user2",
			Games:   []string{"Game A", "Game C"},
			Regions: []string{"NA"},
			Paired:  false,
		},
		{
			UserID:  "user3",
			Games:   []string{"Game A", "Game D"},
			Regions: []string{"NA"},
			Paired:  false,
		},
		{
			UserID:  "user4",
			Games:   []string{"Game A", "Game E"},
			Regions: []string{"NA"},
			Paired:  false,
		},
	}

	// Test pairing algorithm
	groups := pairingService.FindOptimalPairings(users)

	if len(groups) == 0 {
		t.Fatal("Expected at least one group")
	}

	// Check that the algorithm grouped users properly
	for _, group := range groups {
		if len(group) != 4 {
			t.Fatalf("Expected groups of 4, got %d", len(group))
		}

		// Log common games across all group members
		if len(group) > 0 {
			commonGames := group[0].Games
			for i := 1; i < len(group); i++ {
				commonGames = pairingService.FindCommonGames(commonGames, group[i].Games)
			}
			var userIDs []string
			for _, user := range group {
				userIDs = append(userIDs, user.UserID)
			}
			t.Logf("Group %v has common games: %v", userIDs, commonGames)
		}
	}
}

func TestCommonGames(t *testing.T) {
	cfg := config.NewMockConfig(map[string]interface{}{
		"bot_token":         "test_token",
		"igdb_client_id":    "test_id",
		"igdb_client_token": "test_token",
	})

	// Create a temporary database
	dbPath := "test_common_games.db"
	defer func() { _ = os.Remove(dbPath) }()

	db, err := database.NewDB(dbPath)
	if err != nil {
		t.Fatalf("Failed to create database: %v", err)
	}
	defer func() { _ = db.Close() }()

	// Create pairing service (session can be nil for this test)
	pairingService := pairing.NewPairingService(nil, cfg, db)

	games1 := []string{"Game A", "Game B", "Game C"}
	games2 := []string{"Game B", "Game C", "Game D"}

	common := pairingService.FindCommonGames(games1, games2)

	expected := []string{"Game B", "Game C"}
	if len(common) != len(expected) {
		t.Fatalf("Expected %d common games, got %d", len(expected), len(common))
	}

	for i, game := range expected {
		if common[i] != game {
			t.Fatalf("Expected game %s at index %d, got %s", game, i, common[i])
		}
	}
}
