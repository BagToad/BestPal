package time

import (
	"fmt"
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the time command
type TimeModule struct{}

// New creates a new time module
func New() *TimeModule {
	return &TimeModule{}
}

// Register adds the time command to the command map
func (m *TimeModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["time"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "time",
			Description: "Time-related utilities",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "datetime",
					Description: "Natural language date/time",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionBoolean,
					Name:        "full",
					Description: "Show all timestamp format options",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleTime,
	}
}

// handleTime handles the /time command
func (m *TimeModule) handleTime(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Forward top-level options directly to the parser
	m.handleTimeParse(s, i, i.ApplicationCommandData().Options)
}

// handleTimeParse handles the time parse logic
func (m *TimeModule) handleTimeParse(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if len(options) == 0 {
		embed := &discordgo.MessageEmbed{
			Title:       "❌ Missing Parameter",
			Description: "Please provide a date/time to parse.",
			Color:       utils.Colors.Error(),
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal Bot",
			},
		}
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
		return
	}

	dateString := options[0].StringValue()
	if dateString == "" {
		embed := &discordgo.MessageEmbed{
			Title:       "❌ Invalid Parameter",
			Description: "Please provide a valid date/time to parse.",
			Color:       utils.Colors.Error(),
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal Bot",
			},
		}
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
		return
	}

	fullOutput := false
	if len(options) > 1 {
		fullOutput = options[1].BoolValue()
	}

	parsedUnixTime, err := utils.ResolveDateToUnixTimestamp(dateString)
	if err != nil {
		embed := &discordgo.MessageEmbed{
			Title:       "❌ Parse Error",
			Description: fmt.Sprintf("Failed to parse date/time: `%s`", dateString),
			Color:       utils.Colors.Error(),
			Fields: []*discordgo.MessageEmbedField{
				{
					Name: "📋 Supported Formats",
					Value: "• `15:04 MDT` (time only, assumes today)\n" +
						"• `3:04 PM PDT` (time only, assumes today)\n" +
						"• `2006-01-02 15:04:05 EST`\n" +
						"• `2006-01-02 3:04 PM EST`\n" +
						"• `January 2, 2006 3:04 PM PDT`\n" +
						"• `Jan 2, 2006 3:04 PM MDT`",
					Inline: false,
				},
			},
			Footer: &discordgo.MessageEmbedFooter{
				Text: "GamerPal Bot",
			},
		}
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Embeds: &[]*discordgo.MessageEmbed{embed},
		})
		return
	}

	// Create different Discord timestamp formats
	discordTimestamps := map[string]string{
		"Default":         fmt.Sprintf("<t:%d>", parsedUnixTime),
		"Short Time":      fmt.Sprintf("<t:%d:t>", parsedUnixTime),
		"Long Time":       fmt.Sprintf("<t:%d:T>", parsedUnixTime),
		"Short Date":      fmt.Sprintf("<t:%d:d>", parsedUnixTime),
		"Long Date":       fmt.Sprintf("<t:%d:D>", parsedUnixTime),
		"Short Date/Time": fmt.Sprintf("<t:%d:f>", parsedUnixTime),
		"Long Date/Time":  fmt.Sprintf("<t:%d:F>", parsedUnixTime),
		"Relative Time":   fmt.Sprintf("<t:%d:R>", parsedUnixTime),
	}

	if !fullOutput {
		msgBody := fmt.Sprintf("\"`%s`\" is %s at %s\n",
			dateString,
			discordTimestamps["Relative Time"],
			discordTimestamps["Long Date/Time"])
		_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(msgBody),
		})
		return
	}

	// Create the embed
	embed := *utils.NewEmbed()
	embed.Fields = append(embed.Fields, []*discordgo.MessageEmbedField{
		{
			Name: "",
			Value: fmt.Sprintf("🕰️ %s is %s\n",
				discordTimestamps["Long Date/Time"],
				discordTimestamps["Relative Time"]),
			Inline: false,
		},
		{
			Name:   "",
			Value:  fmt.Sprintf("_Converted from `%s`_", dateString),
			Inline: false,
		},
	}...)
	formatOrder := []string{"Default", "Short Time", "Long Time", "Short Date", "Long Date", "Short Date/Time", "Long Date/Time", "Relative Time"}
	var formatsList strings.Builder
	for _, format := range formatOrder {
		formatsList.WriteString(fmt.Sprintf("• **%s**: `%s` → %s\n", format, discordTimestamps[format], discordTimestamps[format]))
	}

	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "📋 Available Discord Timestamp Formats",
		Value:  formatsList.String(),
		Inline: false,
	})

	_, _ = s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Embeds: &[]*discordgo.MessageEmbed{&embed},
	})
}

// GetServices returns nil as this module has no services requiring initialization
func (m *TimeModule) GetServices() []types.ModuleService {
return nil
}
