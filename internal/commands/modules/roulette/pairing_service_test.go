package roulette

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFindOptimalPairings tests the pairing algorithm
func TestFindOptimalPairings(t *testing.T) {
	service := &PairingService{}

	tests := []struct {
		name           string
		users          []User
		expectedGroups int
		expectedSize   int
	}{
		{
			name: "Four users with common games should form one group",
			users: []User{
				{UserID: "user1", Games: []string{"Game1", "Game2"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1", "Game4"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game1", "Game5"}, Regions: []string{"NA"}},
			},
			expectedGroups: 1,
			expectedSize:   4,
		},
		{
			name: "Eight users should form two groups of four",
			users: []User{
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user5", Games: []string{"Game2"}, Regions: []string{"EU"}},
				{UserID: "user6", Games: []string{"Game2"}, Regions: []string{"EU"}},
				{UserID: "user7", Games: []string{"Game2"}, Regions: []string{"EU"}},
				{UserID: "user8", Games: []string{"Game2"}, Regions: []string{"EU"}},
			},
			expectedGroups: 2,
			expectedSize:   4,
		},
		{
			name: "Three users should not form any groups",
			users: []User{
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"NA"}},
			},
			expectedGroups: 0,
			expectedSize:   0,
		},
		{
			name: "Five users should form one group (leaving one unpaired)",
			users: []User{
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user5", Games: []string{"Game1"}, Regions: []string{"NA"}},
			},
			expectedGroups: 1,
			expectedSize:   4,
		},
		{
			name: "Users with no common games should not form groups",
			users: []User{
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game2"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game3"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game4"}, Regions: []string{"NA"}},
			},
			expectedGroups: 0,
			expectedSize:   0,
		},
		{
			name: "Mixed regions with common games should form group (games only matching)",
			users: []User{
				{UserID: "user1", Games: []string{"Game1", "Game2"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1", "Game4"}, Regions: []string{"EU"}},
				{UserID: "user4", Games: []string{"Game1", "Game5"}, Regions: []string{"EU"}},
			},
			expectedGroups: 1,
			expectedSize:   4,
		},
		{
			name: "Games have spaces in the name",
			users: []User{
				{UserID: "user1", Games: []string{"Game One", "Game Two"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game One", "Game Three"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game One", "Game Four"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game One", "Game Five"}, Regions: []string{"NA"}},
			},
			expectedGroups: 1,
			expectedSize:   4,
		},
		{
			name: "Large complexity scenario with 50 users, some overlapping games and regions",
			users: []User{
				// Group 1: Game1 + NA
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game1"}, Regions: []string{"NA"}},
				// Group 2: Game2 + EU
				{UserID: "user5", Games: []string{"Game2"}, Regions: []string{"EU"}},
				{UserID: "user6", Games: []string{"Game2"}, Regions: []string{"EU"}},
				{UserID: "user7", Games: []string{"Game2"}, Regions: []string{"EU"}},
				{UserID: "user8", Games: []string{"Game2"}, Regions: []string{"EU"}},
				// Group 3: Game3 + AS
				{UserID: "user9", Games: []string{"Game3"}, Regions: []string{"AS"}},
				{UserID: "user10", Games: []string{"Game3"}, Regions: []string{"AS"}},
				{UserID: "user11", Games: []string{"Game3"}, Regions: []string{"AS"}},
				{UserID: "user12", Games: []string{"Game3"}, Regions: []string{"AS"}},
				// Group 4: Game4 + Mixed
				{UserID: "user13", Games: []string{"Game4"}, Regions: []string{"NA"}},
				{UserID: "user14", Games: []string{"Game4"}, Regions: []string{"EU"}},
				{UserID: "user15", Games: []string{"Game4"}, Regions: []string{"AS"}},
				{UserID: "user16", Games: []string{"Game4"}, Regions: []string{"NA"}},
				// Unmatched users :(
				{UserID: "user17", Games: []string{"Game6", "Game18"}, Regions: []string{"EU"}},
				{UserID: "user18", Games: []string{"Game6", "Game19"}, Regions: []string{"AS"}},
				{UserID: "user19", Games: []string{"Game7", "Game20"}, Regions: []string{"NA"}},
				{UserID: "user20", Games: []string{"Game7", "Game21"}, Regions: []string{"EU"}},
				{UserID: "user21", Games: []string{"Game8", "Game22"}, Regions: []string{"AS"}},
				{UserID: "user22", Games: []string{"Game8", "Game23"}, Regions: []string{"NA"}},
				{UserID: "user23", Games: []string{"Game9", "Game24"}, Regions: []string{"EU"}},
				{UserID: "user24", Games: []string{"Game9", "Game25"}, Regions: []string{"AS"}},
				{UserID: "user25", Games: []string{"Game10", "Game26"}, Regions: []string{"NA"}},
				{UserID: "user26", Games: []string{"Game10", "Game27"}, Regions: []string{"EU"}},
				{UserID: "user27", Games: []string{"Game11", "Game28"}, Regions: []string{"AS"}},
				{UserID: "user28", Games: []string{"Game11", "Game29"}, Regions: []string{"NA"}},
				{UserID: "user29", Games: []string{"Game12", "Game30"}, Regions: []string{"EU"}},
				{UserID: "user30", Games: []string{"Game12", "Game31"}, Regions: []string{"AS"}},
				{UserID: "user31", Games: []string{"Game13", "Game32"}, Regions: []string{"NA"}},
				{UserID: "user32", Games: []string{"Game13", "Game33"}, Regions: []string{"EU"}},
				{UserID: "user33", Games: []string{"Game14", "Game34"}, Regions: []string{"AS"}},
				{UserID: "user34", Games: []string{"Game14", "Game35"}, Regions: []string{"NA"}},
				{UserID: "user35", Games: []string{"Game15", "Game36"}, Regions: []string{"EU"}},
				{UserID: "user36", Games: []string{"Game15", "Game37"}, Regions: []string{"AS"}},
				{UserID: "user37", Games: []string{"Game16", "Game38"}, Regions: []string{"NA"}},
				{UserID: "user38", Games: []string{"Game16", "Game39"}, Regions: []string{"EU"}},
				{UserID: "user39", Games: []string{"Game17", "Game40"}, Regions: []string{"AS"}},
				{UserID: "user40", Games: []string{"Game17", "Game41"}, Regions: []string{"NA"}},
				{UserID: "user41", Games: []string{"Game18", "Game42"}, Regions: []string{"EU"}},
				{UserID: "user42", Games: []string{"Game18", "Game43"}, Regions: []string{"AS"}},
				{UserID: "user43", Games: []string{"Game19", "Game44"}, Regions: []string{"NA"}},
				{UserID: "user44", Games: []string{"Game19", "Game45"}, Regions: []string{"EU"}},
				{UserID: "user45", Games: []string{"Game20", "Game46"}, Regions: []string{"AS"}},
				{UserID: "user46", Games: []string{"Game20", "Game47"}, Regions: []string{"NA"}},
				{UserID: "user47", Games: []string{"Game21", "Game48"}, Regions: []string{"EU"}},
				{UserID: "user48", Games: []string{"Game21", "Game49"}, Regions: []string{"AS"}},
				{UserID: "user49", Games: []string{"Game22", "Game50"}, Regions: []string{"NA"}},
				{UserID: "user50", Games: []string{"Game22", "Game51"}, Regions: []string{"EU"}},
			},
			expectedGroups: 4,
			expectedSize:   4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a copy of users to avoid test interference
			usersCopy := make([]User, len(tt.users))
			copy(usersCopy, tt.users)

			groups := service.FindOptimalPairings(usersCopy)

			require.Equal(t, tt.expectedGroups, len(groups), "Expected %d groups, got %d", tt.expectedGroups, len(groups))

			if tt.expectedGroups > 0 {
				for i, group := range groups {
					require.Equal(t, tt.expectedSize, len(group), "Group %d: expected size %d, got %d", i, tt.expectedSize, len(group))

					// Verify all users in the group have at least one common game
					if len(group) > 1 {
						anchorGames := group[0].Games
						for j := 1; j < len(group); j++ {
							commonGames := service.FindCommonGames(anchorGames, group[j].Games)
							require.Greater(t, len(commonGames), 0, "Users in group should have at least one common game")
						}
					}
				}
			}

			// Verify that paired users are marked as paired
			pairedCount := 0
			for _, user := range usersCopy {
				if user.Paired {
					pairedCount++
				}
			}
			expectedPaired := tt.expectedGroups * tt.expectedSize
			require.Equal(t, expectedPaired, pairedCount, "Expected %d users to be paired, got %d", expectedPaired, pairedCount)
		})
	}
}

// TestComplexPairingScenarios tests more complex scenarios
func TestComplexPairingScenarios(t *testing.T) {
	service := &PairingService{}

	t.Run("12 users with overlapping games should form multiple groups", func(t *testing.T) {
		users := []User{
			// Group 1: Game1 + NA
			{UserID: "user1", Games: []string{"Game1", "Game2"}, Regions: []string{"NA"}},
			{UserID: "user2", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
			{UserID: "user3", Games: []string{"Game1", "Game4"}, Regions: []string{"NA"}},
			{UserID: "user4", Games: []string{"Game1", "Game5"}, Regions: []string{"NA"}},
			// Group 2: Game2 + EU
			{UserID: "user5", Games: []string{"Game2", "Game6"}, Regions: []string{"EU"}},
			{UserID: "user6", Games: []string{"Game2", "Game7"}, Regions: []string{"EU"}},
			{UserID: "user7", Games: []string{"Game2", "Game8"}, Regions: []string{"EU"}},
			{UserID: "user8", Games: []string{"Game2", "Game9"}, Regions: []string{"EU"}},
			// Group 3: Game3 + AS
			{UserID: "user9", Games: []string{"Game3", "Game10"}, Regions: []string{"AS"}},
			{UserID: "user10", Games: []string{"Game3", "Game11"}, Regions: []string{"AS"}},
			{UserID: "user11", Games: []string{"Game3", "Game12"}, Regions: []string{"AS"}},
			{UserID: "user12", Games: []string{"Game3", "Game13"}, Regions: []string{"AS"}},
		}

		groups := service.FindOptimalPairings(users)
		require.Equal(t, 3, len(groups), "Expected 3 groups from 12 users")

		for i, group := range groups {
			require.Equal(t, 4, len(group), "Group %d should have 4 users", i)
		}
	})

	t.Run("6 users where only 4 can be grouped", func(t *testing.T) {
		users := []User{
			// These 4 can form a group
			{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
			{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"NA"}},
			{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"NA"}},
			{UserID: "user4", Games: []string{"Game1"}, Regions: []string{"NA"}},
			// These 2 cannot form a group with anyone
			{UserID: "user5", Games: []string{"Game2"}, Regions: []string{"EU"}},
			{UserID: "user6", Games: []string{"Game3"}, Regions: []string{"AS"}},
		}

		groups := service.FindOptimalPairings(users)
		require.Equal(t, 1, len(groups), "Expected 1 group from 6 users")
		require.Equal(t, 4, len(groups[0]), "Group should have 4 users")

		// Check that user5 and user6 are not paired
		pairedCount := 0
		for _, user := range users {
			if user.Paired {
				pairedCount++
			}
		}
		require.Equal(t, 4, pairedCount, "Only 4 users should be paired")
	})
}

// TestFormGroup tests the formGroup function specifically
func TestFormGroup(t *testing.T) {
	service := &PairingService{}

	tests := []struct {
		name     string
		users    []User
		expected int // expected group size (0 if no group should be formed)
	}{
		{
			name: "Four users with common games and regions should form group",
			users: []User{
				{UserID: "user1", Games: []string{"Game1", "Game2"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1", "Game4"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game1", "Game5"}, Regions: []string{"NA"}},
			},
			expected: 4,
		},
		{
			name: "Four users with common games but mixed regions should form group",
			users: []User{
				{UserID: "user1", Games: []string{"Game1", "Game2"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1", "Game4"}, Regions: []string{"EU"}},
				{UserID: "user4", Games: []string{"Game1", "Game5"}, Regions: []string{"EU"}},
			},
			expected: 4,
		},
		{
			name: "Less than 4 users should not form group",
			users: []User{
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"NA"}},
			},
			expected: 0,
		},
		{
			name: "No common games should not form group",
			users: []User{
				{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game2"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game3"}, Regions: []string{"NA"}},
				{UserID: "user4", Games: []string{"Game4"}, Regions: []string{"NA"}},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := service.formGroup(tt.users)

			if tt.expected == 0 {
				require.Nil(t, group, "Expected no group to be formed")
			} else {
				require.NotNil(t, group, "Expected a group to be formed")
				require.Equal(t, tt.expected, len(group), "Expected group size %d, got %d", tt.expected, len(group))
			}
		})
	}
}

// TestFormGroupFromCandidates tests the formGroupFromCandidates function
func TestFormGroupFromCandidates(t *testing.T) {
	service := &PairingService{}

	anchor := User{UserID: "anchor", Games: []string{"Game1", "Game2"}, Regions: []string{"NA"}}

	tests := []struct {
		name       string
		candidates []User
		expected   int // expected group size (includes anchor)
	}{
		{
			name: "Three candidates with common games should form group of 4",
			candidates: []User{
				{UserID: "user1", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1", "Game4"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game1", "Game5"}, Regions: []string{"NA"}},
			},
			expected: 4,
		},
		{
			name: "Two candidates should not form group",
			candidates: []User{
				{UserID: "user1", Games: []string{"Game1", "Game3"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game1", "Game4"}, Regions: []string{"NA"}},
			},
			expected: 0,
		},
		{
			name: "Candidates with no common games should not form group",
			candidates: []User{
				{UserID: "user1", Games: []string{"Game3"}, Regions: []string{"NA"}},
				{UserID: "user2", Games: []string{"Game4"}, Regions: []string{"NA"}},
				{UserID: "user3", Games: []string{"Game5"}, Regions: []string{"NA"}},
			},
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group := service.formGroupFromCandidates(anchor, tt.candidates)

			if tt.expected == 0 {
				require.Nil(t, group, "Expected no group to be formed")
			} else {
				require.NotNil(t, group, "Expected a group to be formed")
				require.Equal(t, tt.expected, len(group), "Expected group size %d, got %d", tt.expected, len(group))
				require.Equal(t, "anchor", group[0].UserID, "First user should be the anchor")
			}
		})
	}
}
func TestFindCommonGames(t *testing.T) {
	service := &PairingService{}

	tests := []struct {
		name     string
		games1   []string
		games2   []string
		expected []string
	}{
		{
			name:     "Common games found",
			games1:   []string{"Game1", "Game2", "Game3"},
			games2:   []string{"Game2", "Game3", "Game4"},
			expected: []string{"Game2", "Game3"},
		},
		{
			name:     "No common games",
			games1:   []string{"Game1", "Game2"},
			games2:   []string{"Game3", "Game4"},
			expected: []string{},
		},
		{
			name:     "All games common",
			games1:   []string{"Game1", "Game2"},
			games2:   []string{"Game1", "Game2"},
			expected: []string{"Game1", "Game2"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.FindCommonGames(tt.games1, tt.games2)

			if len(result) != len(tt.expected) {
				t.Errorf("Expected %d common games, got %d", len(tt.expected), len(result))
				return
			}

			for i, game := range result {
				if game != tt.expected[i] {
					t.Errorf("Expected game %s at index %d, got %s", tt.expected[i], i, game)
				}
			}
		})
	}
}

// TestHasCommonRegion tests the common region checking logic
func TestHasCommonRegion(t *testing.T) {
	service := &PairingService{}

	tests := []struct {
		name     string
		regions1 []string
		regions2 []string
		expected bool
	}{
		{
			name:     "Common region found",
			regions1: []string{"NA", "EU"},
			regions2: []string{"EU", "AS"},
			expected: true,
		},
		{
			name:     "No common region",
			regions1: []string{"NA", "EU"},
			regions2: []string{"AS", "OC"},
			expected: false,
		},
		{
			name:     "Same regions",
			regions1: []string{"NA"},
			regions2: []string{"NA"},
			expected: true,
		},
		{
			name:     "Empty regions",
			regions1: []string{},
			regions2: []string{"NA"},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := service.hasCommonRegion(tt.regions1, tt.regions2)

			if result != tt.expected {
				t.Errorf("Expected %v, got %v", tt.expected, result)
			}
		})
	}
}

// TestGamesPriorityMatching tests that games take priority over regions in matching
func TestGamesPriorityMatching(t *testing.T) {
	service := &PairingService{}

	t.Run("Should form group with game matches over region matches", func(t *testing.T) {
		users := []User{
			// These 4 share games but have different regions
			{UserID: "user1", Games: []string{"Game1"}, Regions: []string{"NA"}},
			{UserID: "user2", Games: []string{"Game1"}, Regions: []string{"EU"}},
			{UserID: "user3", Games: []string{"Game1"}, Regions: []string{"AS"}},
			{UserID: "user4", Games: []string{"Game1"}, Regions: []string{"OC"}},
			// These 4 share regions but no games
			{UserID: "user5", Games: []string{"Game2"}, Regions: []string{"NA"}},
			{UserID: "user6", Games: []string{"Game3"}, Regions: []string{"NA"}},
			{UserID: "user7", Games: []string{"Game4"}, Regions: []string{"NA"}},
			{UserID: "user8", Games: []string{"Game5"}, Regions: []string{"NA"}},
		}

		groups := service.FindOptimalPairings(users)

		// Should form one group with the users who share games
		require.Equal(t, 1, len(groups), "Expected 1 group")
		require.Equal(t, 4, len(groups[0]), "Group should have 4 users")

		// Verify the group contains users with common games
		groupUserIDs := make(map[string]bool)
		for _, user := range groups[0] {
			groupUserIDs[user.UserID] = true
		}

		// Should include the users with Game1 (user1-user4)
		gameMatchCount := 0
		for _, userID := range []string{"user1", "user2", "user3", "user4"} {
			if groupUserIDs[userID] {
				gameMatchCount++
			}
		}
		require.Equal(t, 4, gameMatchCount, "All 4 users with common games should be grouped")
	})
}

// TestComplexGroupFormation tests complex scenarios where groups need careful formation
func TestComplexGroupFormation(t *testing.T) {
	service := &PairingService{}

	t.Run("Complex overlapping games scenario", func(t *testing.T) {
		users := []User{
			{UserID: "user1", Games: []string{"A", "B", "C"}, Regions: []string{"NA"}},
			{UserID: "user2", Games: []string{"A", "D", "E"}, Regions: []string{"NA"}},
			{UserID: "user3", Games: []string{"A", "F", "G"}, Regions: []string{"NA"}},
			{UserID: "user4", Games: []string{"A", "H", "I"}, Regions: []string{"NA"}},
			{UserID: "user5", Games: []string{"B", "J", "K"}, Regions: []string{"EU"}},
			{UserID: "user6", Games: []string{"B", "L", "M"}, Regions: []string{"EU"}},
		}

		groups := service.FindOptimalPairings(users)

		// Should form one group of 4 with Game A
		require.Equal(t, 1, len(groups), "Expected 1 group")
		require.Equal(t, 4, len(groups[0]), "Group should have 4 users")

		// Verify all users in group share Game A
		for _, user := range groups[0] {
			require.Contains(t, user.Games, "A", "All grouped users should have Game A")
		}
	})

	t.Run("Regional and game preference ordering", func(t *testing.T) {
		users := []User{
			// Perfect match: same games + same region
			{UserID: "perfect1", Games: []string{"GameX"}, Regions: []string{"NA"}},
			{UserID: "perfect2", Games: []string{"GameX"}, Regions: []string{"NA"}},
			{UserID: "perfect3", Games: []string{"GameX"}, Regions: []string{"NA"}},
			{UserID: "perfect4", Games: []string{"GameX"}, Regions: []string{"NA"}},
			// Game match but different region
			{UserID: "mixed1", Games: []string{"GameX"}, Regions: []string{"EU"}},
			{UserID: "mixed2", Games: []string{"GameX"}, Regions: []string{"AS"}},
		}

		groups := service.FindOptimalPairings(users)

		require.Equal(t, 1, len(groups), "Expected 1 group")
		require.Equal(t, 4, len(groups[0]), "Group should have 4 users")

		// Check that all users have the common game
		for _, user := range groups[0] {
			require.Contains(t, user.Games, "GameX", "All users should have GameX")
		}
	})
}
