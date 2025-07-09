package commands

import (
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestHandler creates a handler for testing
func createTestHandler() *Handler {
	cfg := &config.Config{
		IGDBClientID:    "test_id",
		IGDBClientToken: "test_token",
	}
	return NewHandler(cfg)
}

func TestFormatReleaseDate(t *testing.T) {
	tests := []struct {
		name      string
		timestamp int
		expected  string
	}{
		{
			name:      "zero timestamp returns TBA",
			timestamp: 0,
			expected:  "TBA",
		},
		{
			name:      "valid timestamp formats correctly",
			timestamp: 1609459200, // This will vary by timezone, so we'll test format structure
			expected:  "",         // We'll verify this separately
		},
		{
			name:      "another valid timestamp",
			timestamp: 1577836800, // This will vary by timezone
			expected:  "",         // We'll verify this separately
		},
		{
			name:      "release date in past",
			timestamp: 1431993600, // This will vary by timezone
			expected:  "",         // We'll verify this separately
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatReleaseDate(tt.timestamp)

			if tt.expected == "TBA" {
				assert.Equal(t, tt.expected, result)
			} else {
				// For non-zero timestamps, verify the format is correct (Month Day, Year)
				// but don't check exact values due to timezone differences
				assert.Regexp(t, `^[A-Z][a-z]+ \d{1,2}, \d{4}$`, result)
				assert.NotEqual(t, "TBA", result)
			}
		})
	}
}

func TestNewGameEmbed_BasicStructure(t *testing.T) {
	handler := createTestHandler()

	t.Run("basic embed with minimal data", func(t *testing.T) {
		options := gameEmbedOptions{
			Name:       "Test Game",
			IGDBClient: handler.igdbClient,
		}

		embed := newGameEmbed(options)

		assert.Equal(t, "🎮 Test Game", embed.Title)
		assert.Equal(t, utils.Colors.Fancy(), embed.Color)
		require.NotNil(t, embed.Footer)
		assert.Empty(t, embed.Description)
		assert.Empty(t, embed.Fields)
	})

	t.Run("embed with summary", func(t *testing.T) {
		summary := "This is a test game summary"
		options := gameEmbedOptions{
			Name:       "Test Game",
			Summary:    summary,
			IGDBClient: handler.igdbClient,
		}

		embed := newGameEmbed(options)

		assert.Equal(t, summary, embed.Description)
	})

	t.Run("embed with long summary gets truncated", func(t *testing.T) {
		longSummary := make([]byte, 1030)
		for i := range longSummary {
			longSummary[i] = 'a'
		}

		options := gameEmbedOptions{
			Name:       "Test Game",
			Summary:    string(longSummary),
			IGDBClient: handler.igdbClient,
		}

		embed := newGameEmbed(options)

		assert.Equal(t, 1024, len(embed.Description))
		assert.True(t, embed.Description[len(embed.Description)-3:] == "...")
	})

	t.Run("embed with release date", func(t *testing.T) {
		releaseDate := 1609459200 // Around January 1, 2021 (timezone dependent)
		options := gameEmbedOptions{
			Name:             "Test Game",
			FirstReleaseDate: releaseDate,
			IGDBClient:       handler.igdbClient,
		}

		embed := newGameEmbed(options)

		require.Len(t, embed.Fields, 1)
		assert.Equal(t, "📅 Release Date", embed.Fields[0].Name)
		// Check that it's a valid date format, not the specific date due to timezone differences
		assert.Regexp(t, `^[A-Z][a-z]+ \d{1,2}, \d{4}$`, embed.Fields[0].Value)
		assert.True(t, embed.Fields[0].Inline)
	})
}

func TestNewGameEmbed_EdgeCases(t *testing.T) {
	handler := createTestHandler()

	t.Run("empty game name", func(t *testing.T) {
		options := gameEmbedOptions{
			Name:       "",
			IGDBClient: handler.igdbClient,
		}

		embed := newGameEmbed(options)

		assert.Equal(t, "🎮 ", embed.Title)
	})

	t.Run("very long game name", func(t *testing.T) {
		longName := "This is a Very Long Game Name That Might Cause Issues in Discord Embeds"
		options := gameEmbedOptions{
			Name:       longName,
			IGDBClient: handler.igdbClient,
		}

		embed := newGameEmbed(options)

		assert.Equal(t, "🎮 "+longName, embed.Title)
	})

	t.Run("release date at boundaries", func(t *testing.T) {
		// Test edge cases for release dates
		testCases := []struct {
			name      string
			timestamp int
			pattern   string
		}{
			{
				name:      "Unix epoch",
				timestamp: 1,
				pattern:   `^[A-Z][a-z]+ \d{1,2}, 19(69|70)$`, // Allow for timezone variation
			},
			{
				name:      "negative timestamp",
				timestamp: -1,
				pattern:   `^[A-Z][a-z]+ \d{1,2}, 1969$`,
			},
		}

		for _, tc := range testCases {
			t.Run(tc.name, func(t *testing.T) {
				options := gameEmbedOptions{
					Name:             "Test Game",
					FirstReleaseDate: tc.timestamp,
					IGDBClient:       handler.igdbClient,
				}

				embed := newGameEmbed(options)

				require.Len(t, embed.Fields, 1)
				assert.Regexp(t, tc.pattern, embed.Fields[0].Value)
			})
		}
	})
}

func TestGameEmbedOptions_FieldValidation(t *testing.T) {
	handler := createTestHandler()

	t.Run("all fields populated", func(t *testing.T) {
		options := gameEmbedOptions{
			Name:             "Complete Game",
			Summary:          "This game has all the fields filled out for testing",
			FirstReleaseDate: 1609459200,
			Cover:            123,
			Websites: map[string]string{
				"Official": "https://example.com",
				"Steam":    "https://store.steampowered.com/app/123456/Complete_Game/",
				"GOG":      "https://www.gog.com/game/complete_game",
			},
			MultiplayerModes: []int{1},
			Genres:           []int{12, 31},
			IGDBClient:       handler.igdbClient,
		}

		// Test that the struct can be created with all fields
		assert.Equal(t, "Complete Game", options.Name)
		assert.NotEmpty(t, options.Summary)
		assert.NotZero(t, options.FirstReleaseDate)
		assert.NotZero(t, options.Cover)
		assert.NotEmpty(t, options.Websites)
		assert.NotEmpty(t, options.MultiplayerModes)
		assert.NotEmpty(t, options.Genres)
		assert.NotNil(t, options.IGDBClient)

		// Test that the embed can be created
		embed := newGameEmbed(options)
		assert.NotNil(t, embed)
		assert.Equal(t, "🎮 Complete Game", embed.Title)
	})
}

func TestHandlerCreation(t *testing.T) {
	t.Run("handler creation with valid config", func(t *testing.T) {
		cfg := &config.Config{
			IGDBClientID:    "test_client_id",
			IGDBClientToken: "test_client_token",
		}

		handler := NewHandler(cfg)

		assert.NotNil(t, handler)
		assert.NotNil(t, handler.igdbClient)
		assert.NotNil(t, handler.commands)
		assert.Equal(t, 0, len(handler.commands))
	})
}

// Integration tests that test the functions without external dependencies
func TestGameEmbedIntegration(t *testing.T) {
	handler := createTestHandler()

	t.Run("realistic game data", func(t *testing.T) {
		// Simulate data that would come from IGDB for a popular game
		options := gameEmbedOptions{
			Name:             "The Witcher 3: Wild Hunt",
			Summary:          "The Witcher 3: Wild Hunt is a story-driven, next-generation open world role-playing game set in a visually stunning fantasy universe full of meaningful choices and impactful consequences.",
			FirstReleaseDate: 1431993600, // May 19, 2015
			Cover:            12345,
			Websites: map[string]string{
				"Official": "https://thewitcher.com/en/witcher3",
			},
			MultiplayerModes: []int{1},
			Genres:           []int{12, 31}, // RPG, Adventure
			IGDBClient:       handler.igdbClient,
		}

		embed := newGameEmbed(options)

		// Test basic structure
		assert.Equal(t, "🎮 The Witcher 3: Wild Hunt", embed.Title)
		assert.Contains(t, embed.Description, "The Witcher 3")
		assert.Equal(t, 0xffcfd2, embed.Color)

		// Test that release date field is added
		foundReleaseDate := false
		for _, field := range embed.Fields {
			if field.Name == "📅 Release Date" {
				foundReleaseDate = true
				assert.Regexp(t, `^[A-Z][a-z]+ \d{1,2}, 201[5-6]$`, field.Value)
			}
		}
		assert.True(t, foundReleaseDate, "Release date field should be present")
	})

	t.Run("indie game with minimal data", func(t *testing.T) {
		options := gameEmbedOptions{
			Name:       "Indie Game",
			Summary:    "A small indie game",
			IGDBClient: handler.igdbClient,
		}

		embed := newGameEmbed(options)

		assert.Equal(t, "🎮 Indie Game", embed.Title)
		assert.Equal(t, "A small indie game", embed.Description)
		assert.Empty(t, embed.Fields) // No additional fields for minimal data
	})
}