package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"
	"time"

	"github.com/Henry-Sarabia/igdb/v2"
	"github.com/bwmarrin/discordgo"
)

// handleGame handles the game lookup slash command
func (h *Handler) handleGame(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get the game name from the command options
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please provide a game name to search for."),
		})
		return
	}

	gameName := options[0].StringValue()
	if gameName == "" {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please provide a valid game name to search for."),
		})
		return
	}

	// Search for the game using IGDB
	result := searchGame(h, gameName)

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{result},
	})
}

// searchGame searches for a game using IGDB API and returns an embed
func searchGame(h *Handler, gameName string) *discordgo.MessageEmbed {
	// Search for games using the IGDB API with OR condition for name and slug
	games, err := h.igdbClient.Games.Index(
		igdb.SetFilter("name", igdb.OpEquals, gameName),
		igdb.SetFields("name", "summary", "first_release_date", "cover", "websites", "multiplayer_modes"),
	)

	// If no results, try a search
	if err != nil || len(games) == 0 {
		games, err = h.igdbClient.Games.Search(
			gameName,
			igdb.SetFields("name", "summary", "first_release_date", "cover", "websites", "multiplayer_modes", "genres"),
			igdb.SetLimit(1),
		)
	}
	if err != nil && !strings.Contains(err.Error(), "results are empty") {
		return &discordgo.MessageEmbed{
			Title:       "❌ Error",
			Description: fmt.Sprintf("Encountered an error while searching for game: `%s`\n```%s```", gameName, err.Error()),
			Color:       utils.Colors.Error(),
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal Bot",
			},
		}
	}

	if len(games) == 0 {
		return &discordgo.MessageEmbed{
			Title:       "🔍 No Results",
			Description: fmt.Sprintf("No games found matching: **%s**", gameName),
			Color:       utils.Colors.Info(),
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal Bot",
			},
		}
	}

	game := games[0]

	// Create the embed
	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("🎮 %s", game.Name),
		Color: utils.Colors.Fancy(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Bot • Data from IGDB",
		},
	}

	// Add summary if available
	if game.Summary != "" {
		// Truncate summary if it's too long
		summary := game.Summary
		if len(summary) > 1024 {
			summary = summary[:1021] + "..."
		}
		embed.Description = summary
	}

	// Add release date if available
	if game.FirstReleaseDate != 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "📅 Release Date",
			Value:  formatReleaseDate(game.FirstReleaseDate),
			Inline: true,
		})
	}

	// Add Steam URL if available
	if len(game.Websites) > 0 {
		// Get detailed website information
		websites, err := h.igdbClient.Websites.List(game.Websites, igdb.SetFields("url", "category"))
		if err == nil {
			// Look for Steam website (category 1 is Steam)
			for _, website := range websites {
				if website.Category == 13 { // Steam category
					embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
						Name:   "🛒 Steam",
						Value:  website.URL,
						Inline: true,
					})
					break
				}
			}
		}
	}

	// Add multiplayer information if available
	if len(game.MultiplayerModes) > 0 {
		// Get detailed multiplayer mode information
		multiplayerModes, err := h.igdbClient.MultiplayerModes.List(game.MultiplayerModes, igdb.SetFields("*"))
		if err == nil && len(multiplayerModes) > 0 {
			var onlinemax int
			var onlinecoopmax int

			for _, mode := range multiplayerModes {
				if mode.Onlinemax > onlinemax {
					onlinemax = mode.Onlinemax
				}
				if mode.Onlinecoopmax > onlinecoopmax {
					onlinecoopmax = mode.Onlinecoopmax
				}
			}
			var multiplayerText string

			isMultiplayer := onlinemax > 0 || onlinecoopmax > 0
			if isMultiplayer {
				if onlinemax > 0 {
					multiplayerText = fmt.Sprintf("Max %d players", onlinemax)
				} else if onlinecoopmax > 0 {
					multiplayerText = fmt.Sprintf("Co-op up to %d players", onlinecoopmax)
				}
			} else {
				multiplayerText = "No data"
			}

			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:   "🌐 Online Multiplayer",
				Value:  multiplayerText,
				Inline: true,
			})
		}
	}

	// Add the genres if available
	if len(game.Genres) > 0 {
		// Get detailed genre information
		genres, err := h.igdbClient.Genres.List(game.Genres, igdb.SetFields("name"))
		if err == nil && len(genres) > 0 {
			var genreNames []string
			for _, genre := range genres {
				genreNames = append(genreNames, genre.Name)
			}
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:   "🎮 Genres",
				Value:  strings.Join(genreNames, ", "),
				Inline: true,
			})
		}
	}

	// Add cover image if available
	if game.Cover != 0 {
		// Get cover information
		cover, err := h.igdbClient.Covers.Get(game.Cover, igdb.SetFields("image_id"))
		if err == nil {
			// Generate cover image URL using IGDB's image service
			imageURL, err := cover.SizedURL(igdb.SizeCoverSmall, 1)
			if err == nil {
				embed.Image = &discordgo.MessageEmbedImage{
					URL: imageURL,
				}
			}
		}
	}

	return embed
}

// convertSlugName converts a game name to a slug format (lowercase, spaces to hyphens)
func convertSlugName(name string) string {
	// Convert to lowercase and replace spaces/special characters with hyphens
	slug := strings.ToLower(name)
	slug = strings.ReplaceAll(slug, " ", "-")
	slug = strings.ReplaceAll(slug, "'", "")
	slug = strings.ReplaceAll(slug, ":", "")
	slug = strings.ReplaceAll(slug, ".", "")
	slug = strings.ReplaceAll(slug, ",", "")
	slug = strings.ReplaceAll(slug, "!", "")
	slug = strings.ReplaceAll(slug, "?", "")
	return slug
}

// formatReleaseDate converts Unix timestamp to human readable date
func formatReleaseDate(timestamp int) string {
	if timestamp == 0 {
		return "TBA"
	}
	t := time.Unix(int64(timestamp), 0)
	return t.Format("January 2, 2006")
}
