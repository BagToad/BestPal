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

// gameEmbedOptions contains all the data needed to create a game embed
type gameEmbedOptions struct {
	Name             string
	Summary          string
	FirstReleaseDate int
	Cover            int
	Websites         map[string]string
	MultiplayerModes []int
	Genres           []int
	IGDBClient       *igdb.Client
}

// newGameEmbed creates a Discord embed for a game using the provided options
func newGameEmbed(options gameEmbedOptions) *discordgo.MessageEmbed {
	// Create the embed
	embed := &discordgo.MessageEmbed{
		Title: fmt.Sprintf("🎮 %s", options.Name),
		Color: utils.Colors.Fancy(),
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal Bot • Data from IGDB",
		},
	}

	// Add summary if available
	if options.Summary != "" {
		// Truncate summary if it's too long
		summary := options.Summary
		if len(summary) > 1024 {
			summary = summary[:1021] + "..."
		}
		embed.Description = summary
	}

	// Add release date if available
	if options.FirstReleaseDate != 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "📅 Release Date",
			Value:  formatReleaseDate(options.FirstReleaseDate),
			Inline: true,
		})
	}

	// Add websites if available
	if len(options.Websites) > 0 {
		websitesEmbedField := &discordgo.MessageEmbedField{
			Name:   "🛒 Sites",
			Value:  "",
			Inline: true,
		}
		for siteName, URL := range options.Websites {
			websitesEmbedField.Value += fmt.Sprintf("[%s](%s)\n", siteName, URL)
		}

		if websitesEmbedField.Value != "" {
			embed.Fields = append(embed.Fields, websitesEmbedField)
		}
	}

	// Add multiplayer information if available
	if len(options.MultiplayerModes) > 0 {
		// Get detailed multiplayer mode information
		multiplayerModes, err := options.IGDBClient.MultiplayerModes.List(options.MultiplayerModes, igdb.SetFields("*"))
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
	if len(options.Genres) > 0 {
		// Get detailed genre information
		genres, err := options.IGDBClient.Genres.List(options.Genres, igdb.SetFields("name"))
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
	if options.Cover != 0 {
		// Get cover information
		cover, err := options.IGDBClient.Covers.Get(options.Cover, igdb.SetFields("image_id"))
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

// searchGame searches for a game using IGDB API and returns an embed
func searchGame(h *Handler, gameName string) *discordgo.MessageEmbed {
	gameFields := []string{"name", "summary", "first_release_date", "cover", "websites", "multiplayer_modes", "genres"}

	// Get an exact match first
	games, err := h.igdbClient.Games.Index(
		igdb.SetFilter("name", igdb.OpEquals, gameName),
		igdb.SetFields(gameFields...),
	)

	// If no results, try a search
	if err != nil || len(games) == 0 {
		games, err = h.igdbClient.Games.Search(
			gameName,
			igdb.SetFields(gameFields...),
			igdb.SetLimit(1),
		)
	}
	if err != nil && !strings.Contains(err.Error(), "results are empty") {
		return utils.NewErrorEmbed(fmt.Sprintf("Encountered an error while searching for game: `%s`", gameName), err)
	}

	if len(games) == 0 {
		return utils.NewNoResultsEmbed(fmt.Sprintf("No games found matching: **%s**", gameName))
	}

	game := games[0]
	options := gameEmbedOptions{
		Name:             game.Name,
		Summary:          game.Summary,
		FirstReleaseDate: game.FirstReleaseDate,
		Cover:            game.Cover,
		MultiplayerModes: game.MultiplayerModes,
		Genres:           game.Genres,
		IGDBClient:       h.igdbClient,
	}

	// Get detailed website information
	websites, err := h.igdbClient.Websites.List(game.Websites, igdb.SetFields("url", "category"))
	if err == nil {
		for _, website := range websites {
			switch website.Category {
			case igdb.WebsiteSteam:
				options.Websites["Steam"] = website.URL
				continue
			case igdb.WebsiteOfficial:
				options.Websites["Official"] = website.URL
				continue
			case 17: // GOG.com
				options.Websites["GOG"] = website.URL
				continue
			}
		}
	}

	return newGameEmbed(options)
}

// formatReleaseDate converts Unix timestamp to human readable date
func formatReleaseDate(timestamp int) string {
	if timestamp == 0 {
		return "TBA"
	}
	t := time.Unix(int64(timestamp), 0)
	return t.Format("January 2, 2006")
}
