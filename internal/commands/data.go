package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// handleData handles the data slash command with subcommands for storing and fetching data
func (h *Handler) handleData(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Check if database is available
	if h.DB == nil {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Database is not available. Please contact an administrator.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	// Get the subcommand
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Please specify a subcommand. Use `/data store` to store data or `/data fetch` to retrieve data.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	subcommand := options[0]
	switch subcommand.Name {
	case "store":
		h.handleDataStore(s, i, subcommand.Options)
	case "fetch":
		h.handleDataFetch(s, i, subcommand.Options)
	default:
		s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "❌ Unknown subcommand. Available subcommands: `store`, `fetch`",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
	}
}

// handleDataStore handles the data store subcommand
func (h *Handler) handleDataStore(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	// Acknowledge the interaction immediately
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	if len(options) < 2 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Both key and value are required for storing data."),
		})
		return
	}

	key := options[0].StringValue()
	value := options[1].StringValue()

	if key == "" || value == "" {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Key and value cannot be empty."),
		})
		return
	}

	// Store the data in the database using the user's Discord ID
	userID := i.User.ID
	err := h.DB.StoreUserData(userID, key, value)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(fmt.Sprintf("❌ Failed to store data: %v", err)),
		})
		return
	}

	// Create success embed
	embed := &discordgo.MessageEmbed{
		Title:       "✅ Data Stored Successfully",
		Description: "Your data has been stored in the SQLite database.",
		Color:       utils.Colors.Ok(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🔑 Key",
				Value:  fmt.Sprintf("`%s`", key),
				Inline: true,
			},
			{
				Name:   "💾 Value",
				Value:  fmt.Sprintf("`%s`", value),
				Inline: true,
			},
			{
				Name:   "👤 User ID",
				Value:  fmt.Sprintf("`%s`", userID),
				Inline: false,
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "GamerPal SQLite Demo",
		},
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{embed},
	})
}

// handleDataFetch handles the data fetch subcommand
func (h *Handler) handleDataFetch(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	// Acknowledge the interaction immediately
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})

	userID := i.User.ID

	// Check if a specific key was provided
	var key string
	if len(options) > 0 && options[0].StringValue() != "" {
		key = options[0].StringValue()
		// Fetch specific key-value pair
		data, err := h.DB.GetUserData(userID, key)
		if err != nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr(fmt.Sprintf("❌ Failed to fetch data: %v", err)),
			})
			return
		}

		if data == nil {
			embed := &discordgo.MessageEmbed{
				Title:       "🔍 No Data Found",
				Description: fmt.Sprintf("No data found for key `%s`", key),
				Color:       utils.Colors.Warning(),
				Footer: &discordgo.MessageEmbedFooter{
					Text: "GamerPal SQLite Demo",
				},
			}
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// Create embed for single data item
		embed := &discordgo.MessageEmbed{
			Title:       "📊 Data Retrieved",
			Description: "Here's your stored data:",
			Color:       utils.Colors.Info(),
			Fields: []*discordgo.MessageEmbedField{
				{
					Name:   "🔑 Key",
					Value:  fmt.Sprintf("`%s`", data.Key),
					Inline: true,
				},
				{
					Name:   "💾 Value",
					Value:  fmt.Sprintf("`%s`", data.Value),
					Inline: true,
				},
				{
					Name:   "📅 Created",
					Value:  fmt.Sprintf("<t:%d:R>", data.CreatedAt.Unix()),
					Inline: true,
				},
				{
					Name:   "🔄 Updated",
					Value:  fmt.Sprintf("<t:%d:R>", data.UpdatedAt.Unix()),
					Inline: true,
				},
			},
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal SQLite Demo",
			},
		}

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	} else {
		// Fetch all data for the user
		dataList, err := h.DB.GetAllUserData(userID)
		if err != nil {
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Content: utils.StringPtr(fmt.Sprintf("❌ Failed to fetch data: %v", err)),
			})
			return
		}

		if len(dataList) == 0 {
			embed := &discordgo.MessageEmbed{
				Title:       "🔍 No Data Found",
				Description: "You haven't stored any data yet. Use `/data store` to add some data!",
				Color:       utils.Colors.Warning(),
				Footer: &discordgo.MessageEmbedFooter{
					Text: "GamerPal SQLite Demo",
				},
			}
			s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
				Embeds: &[]*discordgo.MessageEmbed{embed},
			})
			return
		}

		// Create embed for all data
		embed := &discordgo.MessageEmbed{
			Title:       "📊 All Your Data",
			Description: fmt.Sprintf("Found %d stored item(s):", len(dataList)),
			Color:       utils.Colors.Info(),
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal SQLite Demo",
			},
		}

		// Add fields for each data item (limit to prevent embed from being too large)
		maxFields := 10
		for i, data := range dataList {
			if i >= maxFields {
				embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
					Name:   "📋 More Data",
					Value:  fmt.Sprintf("... and %d more items. Use `/data fetch key:<specific_key>` to get individual items.", len(dataList)-maxFields),
					Inline: false,
				})
				break
			}

			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:   fmt.Sprintf("🔑 %s", data.Key),
				Value:  fmt.Sprintf("💾 `%s`\n📅 Updated <t:%d:R>", data.Value, data.UpdatedAt.Unix()),
				Inline: true,
			})
		}

		// Add database stats
		stats, err := h.DB.GetStats()
		if err == nil {
			var statsText strings.Builder
			statsText.WriteString("**Database Statistics:**\n")
			if totalRecords, ok := stats["total_records"].(int); ok {
				statsText.WriteString(fmt.Sprintf("📊 Total Records: %d\n", totalRecords))
			}
			if uniqueUsers, ok := stats["unique_users"].(int); ok {
				statsText.WriteString(fmt.Sprintf("� Unique Users: %d\n", uniqueUsers))
			}
			if lastUpdated, ok := stats["last_updated"].(string); ok && lastUpdated != "No data" {
				statsText.WriteString(fmt.Sprintf("🕒 Last Activity: %s", lastUpdated))
			}

			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:   "📈 Database Info",
				Value:  statsText.String(),
				Inline: false,
			})
		}

		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
	}
}
