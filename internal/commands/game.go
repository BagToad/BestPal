package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"math/rand"
	"strconv"
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

	// Get the game name from the command commandOptions
	commandOptions := i.ApplicationCommandData().Options
	if len(commandOptions) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please provide a game name to search for or use random"),
		})
		return
	}

	var gameNameCommandOption string
	var randomCommandOption bool
	for _, option := range commandOptions {
		switch option.Name {
		case "random":
			if option.BoolValue() {
				randomCommandOption = true
			}
			break
		case "name":
			gameNameCommandOption = option.StringValue()
		}
	}

	if randomCommandOption {
		game := getRandomGame(h.igdbClient)
		if game == nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr("❌ No games found to return randomly."),
			})
			return
		}

		// Create the embed options from the game data
		embedOptions := newGameEmbedOptionsFromGame(h, game)
		embed := newGameEmbed(embedOptions)
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
		return
	}

	if gameNameCommandOption == "" {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please provide a valid game name to search for."),
		})
		return
	}

	// Search for the game using IGDB
	game, err := searchGame(h, gameNameCommandOption)
	if err != nil && !strings.Contains(err.Error(), "results are empty") {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{utils.NewErrorEmbed(fmt.Sprintf("Encountered an error while searching for game: `%s`", gameNameCommandOption), err)},
		})
		return
	}
	if game == nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{utils.NewNoResultsEmbed(fmt.Sprintf("No games found matching: **%s**", gameNameCommandOption))},
		})
		return
	}

	// Create the embed options from the game data
	embedOptions := newGameEmbedOptionsFromGame(h, game)

	// Create the actual embed
	embed := newGameEmbed(embedOptions)

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

// gameEmbedOptions contains all the data needed to create a game embed
type gameEmbedOptions struct {
	Name             string
	Summary          string
	FirstReleaseDate int
	CoverURL         string
	Websites         map[string]string
	MultiplayerModes []igdb.MultiplayerMode
	Genres           []string
	IGDBClient       *igdb.Client
}

var gameFields = []string{"name", "summary", "first_release_date", "cover", "websites", "multiplayer_modes", "genres"}

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
		var onlineMax int
		var onlineCoopMax int

		for _, mode := range options.MultiplayerModes {
			if mode.Onlinemax > onlineMax {
				onlineMax = mode.Onlinemax
			}
			if mode.Onlinecoopmax > onlineCoopMax {
				onlineCoopMax = mode.Onlinecoopmax
			}
		}
		var multiplayerText string

		isMultiplayer := onlineMax > 0 || onlineCoopMax > 0
		if isMultiplayer {
			if onlineMax > 0 {
				multiplayerText = fmt.Sprintf("Max %d players", onlineMax)
			} else if onlineCoopMax > 0 {
				multiplayerText = fmt.Sprintf("Co-op up to %d players", onlineCoopMax)
			}
		}

		if multiplayerText != "" {
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:   "🌐 Online Multiplayer",
				Value:  multiplayerText,
				Inline: true,
			})
		}
	}

	// Add the genres if available
	if len(options.Genres) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "🎮 Genres",
			Value:  strings.Join(options.Genres, ", "),
			Inline: true,
		})
	}

	// Add cover image if available
	if options.CoverURL != "" {
		embed.Image = &discordgo.MessageEmbedImage{
			URL: options.CoverURL,
		}
	}

	return embed
}

func getRandomGame(igdbClient *igdb.Client) *igdb.Game {
	totalGames, err := igdbClient.Games.Count()
	if err != nil || totalGames == 0 {
		return nil
	}

	var randomGameIds []int
	for i := 0; i < 5; i++ {
		randomGameIds = append(randomGameIds, rand.Intn(totalGames))
	}

	fields := append(gameFields, "age_ratings") // Include age ratings to filter out mature games
	var gameIdsFilter []string
	for _, id := range randomGameIds {
		gameIdsFilter = append(gameIdsFilter, strconv.Itoa(id))
	}

	games, err := igdbClient.Games.Index(
		igdb.SetFilter("id", igdb.OpContainsAtLeast, gameIdsFilter...),
		igdb.SetFields(fields...),
		igdb.SetLimit(50),
	)

	for _, game := range games {
		// return the first game that is not rated mature
		if len(game.AgeRatings) > 0 {
			ratings, err := igdbClient.AgeRatings.List(game.AgeRatings, igdb.SetFields("rating"))
			if err != nil || len(ratings) == 0 {
				return nil
			}

			for _, rating := range ratings {
				switch rating.Rating {
				// AgeRatingThree AgeRatingEnum = iota + 1
				// AgeRatingSeven
				// AgeRatingTwelve
				// AgeRatingSixteen
				// AgeRatingEighteen
				// AgeRatingRP
				// AgeRatingEC
				// AgeRatingE
				// AgeRatingE10
				// AgeRatingT
				// AgeRatingM
				// AgeRatingAO
				case igdb.AgeRatingE, igdb.AgeRatingE10, igdb.AgeRatingT, igdb.AgeRatingRP, igdb.AgeRatingEC:
					return game
				}
			}
		}
	}

	if err != nil || len(games) == 0 {
		fmt.Printf("Error fetching random game: %v\n", err)
		return nil
	}

	return games[0]
}

// searchGame searches for a game using IGDB API and returns an embed
func searchGame(h *Handler, gameName string) (*igdb.Game, error) {
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

		if err != nil {
			return nil, err
		}
	}

	return games[0], nil
}

func newGameEmbedOptionsFromGame(h *Handler, game *igdb.Game) gameEmbedOptions {
	options := gameEmbedOptions{
		Name:             game.Name,
		Summary:          game.Summary,
		FirstReleaseDate: game.FirstReleaseDate,
		Websites:         make(map[string]string),
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

	// Get detailed multiplayer modes
	if len(game.MultiplayerModes) > 0 {
		modes, err := h.igdbClient.MultiplayerModes.List(game.MultiplayerModes, igdb.SetFields("*"))
		if err == nil && len(modes) > 0 {
			for _, mode := range modes {
				options.MultiplayerModes = append(options.MultiplayerModes, *mode)
			}
		}
	}

	// Get detailed genre information
	if len(game.Genres) > 0 {
		genres, err := options.IGDBClient.Genres.List(game.Genres, igdb.SetFields("name"))
		if err == nil && len(genres) > 0 {
			for _, genre := range genres {
				options.Genres = append(options.Genres, genre.Name)
			}
		}
	}

	// Get cover image if available
	if game.Cover != 0 {
		cover, err := options.IGDBClient.Covers.Get(game.Cover, igdb.SetFields("image_id"))
		if err == nil {
			// Generate cover image URL using IGDB's image service
			if imageURL, err := cover.SizedURL(igdb.SizeCoverSmall, 1); err == nil {
				options.CoverURL = imageURL
			}
		}
	}

	return options
}

// formatReleaseDate converts Unix timestamp to human readable date
func formatReleaseDate(timestamp int) string {
	if timestamp == 0 {
		return "TBA"
	}
	t := time.Unix(int64(timestamp), 0)
	return t.Format("January 2, 2006")
}
