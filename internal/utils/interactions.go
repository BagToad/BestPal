package utils

import "github.com/bwmarrin/discordgo"

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

// NewNoResultsEmbed creates a new embed for no results found with the given description
func NewNoResultsEmbed(description string) *discordgo.MessageEmbed {
	return &discordgo.MessageEmbed{
		Title:       "🔍 No Results",
		Description: description,
		Color:       Colors.Info(),
		Footer:      standardEmbedFooter,
	}
}
