package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// handleRoulette handles the main roulette command and its subcommands
func (h *SlashHandler) handleRoulette(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Get the subcommand
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please specify a subcommand. Use `/roulette signup`, `/roulette nah`, `/roulette games-add <games>`, or `/roulette games-remove <games>`",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	subcommand := options[0]
	switch subcommand.Name {
	case "signup":
		h.handleRouletteSignup(s, i)
	case "nah":
		h.handleRouletteNah(s, i)
	case "games-add":
		h.handleRouletteGamesAdd(s, i, subcommand.Options)
	case "games-remove":
		h.handleRouletteGamesRemove(s, i, subcommand.Options)
	default:
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unknown subcommand",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}
}

// handleRouletteSignup handles signing up a user for roulette
func (h *SlashHandler) handleRouletteSignup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID
	guildID := i.GuildID

	// Check if already signed up
	isSignedUp, err := h.DB.IsUserSignedUp(userID, guildID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Error checking signup status: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if isSignedUp {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "🎰 You're already signed up for roulette pairing!",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Add the signup
	err = h.DB.AddRouletteSignup(userID, guildID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Error signing up for roulette: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "✅ Successfully signed up for roulette pairing! Use `/roulette games` to add games you want to play.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleRouletteNah handles removing a user from roulette
func (h *SlashHandler) handleRouletteNah(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID
	guildID := i.GuildID

	// Check if signed up
	isSignedUp, err := h.DB.IsUserSignedUp(userID, guildID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Error checking signup status: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if !isSignedUp {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "🤷 You're not signed up for roulette pairing.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Remove the signup and all games
	err = h.DB.RemoveRouletteSignup(userID, guildID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Error removing signup: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "✅ Successfully removed from roulette pairing.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

// handleRouletteGamesAdd handles adding games to a user's roulette list
func (h *SlashHandler) handleRouletteGamesAdd(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	userID := i.Member.User.ID
	guildID := i.GuildID

	// Check if user is signed up
	isSignedUp, err := h.DB.IsUserSignedUp(userID, guildID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Error checking signup status: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if !isSignedUp {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You need to sign up for roulette first using `/roulette signup`",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if len(options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please provide a game name or comma-separated list of games.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer the response since IGDB lookup may take time
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	gameNames := strings.Split(options[0].StringValue(), ",")
	var validGames []string

	type invalidGame struct {
		Name        string
		Suggestions []string
	}
	var invalidGames []invalidGame

	// Lookup each game in IGDB
	for _, gameName := range gameNames {
		gameName = strings.TrimSpace(gameName)
		if gameName == "" {
			continue
		}

		// Search for the game in IGDB
		games, err := h.igdbClient.Games.Search(gameName,
			igdb.SetFields("id", "name"),
			igdb.SetLimit(50),
		)

		if err != nil {
			h.config.Logger.Warn("Failed to fetch game %s: %v", gameName, err)
			invalidGames = append(invalidGames, invalidGame{
				Name:        gameName,
				Suggestions: nil, // No suggestions if IGDB lookup fails
			})
			continue
		}

		var game *igdb.Game
		for _, g := range games {
			if g.Name == "" {
				continue
			}
			if strings.EqualFold(g.Name, gameName) {
				// Found a matching game
				game = g
				break
			}
		}

		if game == nil {
			h.config.Logger.Warn("Failed to exact match for game %s: %v", gameName, err)
			invalidGames = append(invalidGames, invalidGame{
				Name: gameName,
				Suggestions: func() []string {
					// return first x game names as suggestions
					suggestions := make([]string, 0, len(games))
					maxSuggestions := 5

					l := func() int {
						if len(games) < maxSuggestions {
							return len(games)
						}
						return maxSuggestions
					}()
					for _, g := range games[:l] {
						if g.Name != "" {
							suggestions = append(suggestions, g.Name)
						}
					}
					return suggestions
				}(),
			})
			continue
		}

		// Add the game to the user's list
		err = h.DB.AddRouletteGame(userID, guildID, game.Name, game.ID)
		if err != nil {
			invalidGames = append(invalidGames, invalidGame{
				Name:        gameName + " (database error)",
				Suggestions: nil,
			})
			continue
		}

		validGames = append(validGames, game.Name)
	}

	// Build response message
	var response strings.Builder
	response.WriteString("🎮 **Game List Update:**\n\n")

	if len(validGames) > 0 {
		response.WriteString("✅ **Added games:**\n")
		for _, game := range validGames {
			response.WriteString(fmt.Sprintf("• %s\n", game))
		}
		response.WriteString("\n")
	}

	if len(invalidGames) > 0 {
		response.WriteString("❌ **Couldn't find these games:**\n")
		for _, invalidGame := range invalidGames {
			response.WriteString(fmt.Sprintf("• %s", invalidGame.Name))
			if len(invalidGame.Suggestions) > 0 {
				response.WriteString(fmt.Sprintf("(did you mean: %s?)\n", strings.Join(invalidGame.Suggestions, ", ")))
			} else {
				response.WriteString("\n")
			}
		}
		response.WriteString("\n")
	}

	if len(validGames) == 0 && len(invalidGames) == 0 {
		response.WriteString("❌ No games provided.")
	}

	// Get current game list
	currentGames, err := h.DB.GetRouletteGames(userID, guildID)
	if err == nil && len(currentGames) > 0 {
		response.WriteString("📋 **Your current game list:**\n")
		l := func() int {
			if len(currentGames) < 10 {
				return len(currentGames)
			}
			return 10
		}()
		for _, game := range currentGames[:l] { // Show up to 10 games
			response.WriteString(fmt.Sprintf("• %s\n", game.GameName))
		}
		if len(currentGames) > 10 {
			response.WriteString(fmt.Sprintf("... and %d more games\n", len(currentGames[10:])))
		}
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(response.String()),
	})
}

// handleRouletteGamesRemove handles removing games from a user's roulette list
func (h *SlashHandler) handleRouletteGamesRemove(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	userID := i.Member.User.ID
	guildID := i.GuildID

	// Check if user is signed up
	isSignedUp, err := h.DB.IsUserSignedUp(userID, guildID)
	if err != nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Error checking signup status: " + err.Error(),
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if !isSignedUp {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ You need to sign up for roulette first using `/roulette signup`",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if len(options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please provide a game name or comma-separated list of games.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Defer the response
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	gameNames := strings.Split(options[0].StringValue(), ",")
	var removedGames []string
	var notFoundGames []string

	// Get current games list to match against
	currentGames, err := h.DB.GetRouletteGames(userID, guildID)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Error getting current games list: " + err.Error()),
		})
		return
	}

	// Create a map for case-insensitive matching
	gameMap := make(map[string]string)
	for _, game := range currentGames {
		gameMap[strings.ToLower(game.GameName)] = game.GameName
	}

	// Remove each game
	for _, gameName := range gameNames {
		gameName = strings.TrimSpace(gameName)
		if gameName == "" {
			continue
		}

		// Try to find the game (case-insensitive)
		var actualGameName string
		if exactName, exists := gameMap[strings.ToLower(gameName)]; exists {
			actualGameName = exactName
		} else {
			// Try partial matching
			for _, game := range currentGames {
				if strings.Contains(strings.ToLower(game.GameName), strings.ToLower(gameName)) {
					actualGameName = game.GameName
					break
				}
			}
		}

		if actualGameName == "" {
			notFoundGames = append(notFoundGames, gameName)
			continue
		}

		// Remove the game from the database
		err = h.DB.RemoveRouletteGame(userID, guildID, actualGameName)
		if err != nil {
			notFoundGames = append(notFoundGames, gameName+" (database error)")
			continue
		}

		removedGames = append(removedGames, actualGameName)
	}

	// Build response message
	var response strings.Builder
	response.WriteString("🎮 **Game List Update:**\n\n")

	if len(removedGames) > 0 {
		response.WriteString("✅ **Removed games:**\n")
		for _, game := range removedGames {
			response.WriteString(fmt.Sprintf("• %s\n", game))
		}
		response.WriteString("\n")
	}

	if len(notFoundGames) > 0 {
		response.WriteString("❌ **Couldn't find these games:**\n")
		for _, game := range notFoundGames {
			response.WriteString(fmt.Sprintf("• %s\n", game))
		}
		response.WriteString("\n")
	}

	if len(removedGames) == 0 && len(notFoundGames) == 0 {
		response.WriteString("❌ No games provided.")
	}

	// Get updated game list
	updatedGames, err := h.DB.GetRouletteGames(userID, guildID)
	if err == nil {
		if len(updatedGames) > 0 {
			response.WriteString("📋 **Your current game list:**\n")
			for _, game := range updatedGames {
				response.WriteString(fmt.Sprintf("• %s\n", game.GameName))
			}
		} else {
			response.WriteString("📋 **Your game list is now empty.**")
		}
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(response.String()),
	})
}
