package utils

import (
	"fmt"

	"github.com/bwmarrin/discordgo"
)

var standardEmbedFooter = &discordgo.MessageEmbedFooter{
	Text: "Run /help for more options",
}

// NewOKEmbed creates a new OK embed with the given title and description
func NewOKEmbed(title, description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       title,
		Description: description,
		Color:       Colors.Ok(),
		Footer:      standardEmbedFooter,
	}
}

// NewErrorEmbed creates a new error embed with the error as a string.
func NewErrorEmbed(description string, err error) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "❌ Error",
		Description: fmt.Sprintf("%s\n\n```%s```", description, err.Error()),
		Color:       Colors.Error(),
		Footer:      standardEmbedFooter,
	}
}

// NewNoResultsEmbed creates a new embed for no results found with the given description
func NewNoResultsEmbed(description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🔍 No Results",
		Description: description,
		Color:       Colors.Info(),
		Footer:      standardEmbedFooter,
	}
}
