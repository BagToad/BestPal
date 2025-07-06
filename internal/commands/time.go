package commands

import (
	"fmt"
	"gamerpal/internal/utils"
	"strings"

	"github.com/MakeNowJust/heredoc"
	"github.com/markusmobius/go-dateparser"

	"github.com/bwmarrin/discordgo"
)

// handleTime handles the time slash command with subcommands
func (h *Handler) handleTime(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge the interaction immediately
	s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	})

	// Get the subcommand
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please specify a subcommand. Use `/time parse` to parse a date."),
		})
		return
	}

	subcommand := options[0]
	switch subcommand.Name {
	case "parse":
		h.handleTimeParse(s, i, subcommand.Options)
	default:
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Unknown subcommand. Use `/time parse` to parse a date."),
		})
	}
}

// handleTimeParse handles the time parse subcommand
func (h *Handler) handleTimeParse(s *discordgo.Session, i *discordgo.InteractionCreate, options []*discordgo.ApplicationCommandInteractionDataOption) {
	if len(options) == 0 {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please provide a date/time to parse."),
		})
		return
	}

	dateString := options[0].StringValue()
	if dateString == "" {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr("❌ Please provide a valid date/time to parse."),
		})
		return
	}

	fullOutput := false
	if len(options) > 1 {
		fullOutput = options[1].BoolValue()
	}

	parsedUnixTime, err := parseUnixTimestamp(dateString)
	if err != nil {
		s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
			Content: utils.StringPtr(heredoc.Docf(`
				❌ Failed to parse date/time: %[2]s%[1]s%[2]s

				**Supported formats:**
				• %[2]s15:04 MDT%[2]s (time only, assumes today)
				• %[2]s3:04 PM PDT%[2]s (time only, assumes today)
				• %[2]s2006-01-02 15:04:05 EST%[2]s
				• %[2]s2006-01-02 3:04 PM EST%[2]s
				• %[2]sJanuary 2, 2006 3:04 PM PDT%[2]s
				• %[2]sJan 2, 2006 3:04 PM MDT%[2]s
				`, dateString, "`")),
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

	var responseBuilder strings.Builder
	responseBuilder.WriteString(
		heredoc.Docf(`
		🕰️ %s is %s
		
		Converted from: %[4]s%[3]s%[4]s
		`, discordTimestamps["Long Date/Time"], discordTimestamps["Relative Time"], dateString, "`"),
	)

	if fullOutput {
		responseBuilder.WriteString("**Available Discord timestamp formats:**\n")
		formatOrder := []string{"Default", "Short Time", "Long Time", "Short Date", "Long Date", "Short Date/Time", "Long Date/Time", "Relative Time"}
		for _, format := range formatOrder {
			responseBuilder.WriteString(fmt.Sprintf("• **%s**: `%s` → %s\n", format, discordTimestamps[format], discordTimestamps[format]))
		}
	}

	s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: utils.StringPtr(responseBuilder.String()),
	})
}

// parseUnixTimestamp attempts to parse a date/time string and return a unix timestamp.
func parseUnixTimestamp(dateString string) (int64, error) {
	dateString = strings.TrimSpace(dateString)

	dt, err := dateparser.Parse(nil, dateString)
	if err != nil {
		return 0, fmt.Errorf("unable to parse date/time format: %w", err)
	}

	timestamp := dt.Time.Unix()
	return timestamp, nil
}
