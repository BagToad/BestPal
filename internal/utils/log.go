package utils

import (
	"errors"
	"gamerpal/internal/config"
	"io"
	"time"

	"github.com/bwmarrin/discordgo"
)

func LogToChannel(cfg *config.Config, s *discordgo.Session, m string) error {
	logEmbed := &discordgo.MessageEmbed{
		Title:       "Best Pal Message",
		Description: m,
		Color:       Colors.Info(),
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	if id := cfg.GetGamerpalsLogChannelID(); id != "" {
		_, err := s.ChannelMessageSendEmbed(id, logEmbed)
		if err != nil {
			return err
		}
	} else {
		return errors.New("unable to log to channel: gamerpals_log_channel_id is not set")
	}

	return nil
}

// LogToChannelWithEmbedAndFile sends an embed with an optional file attachment to the log channel
func LogToChannelWithEmbedAndFile(cfg *config.Config, s *discordgo.Session, message string, fileName string, fileReader io.Reader) error {
	id := cfg.GetGamerpalsLogChannelID()
	if id == "" {
		return errors.New("unable to log to channel: gamerpals_log_channel_id is not set")
	}

	const maxLen = 900
	if len(message) > maxLen {
		message = message[:maxLen] + "\n...(truncated, see attached file for full list)"
	}

	embed := &discordgo.MessageEmbed{
		Title:       "Best Pal Message",
		Description: message,
		Color:       Colors.Info(),
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	msg := &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{embed},
	}

	if fileReader != nil && fileName != "" {
		msg.Files = []*discordgo.File{
			{
				Name:   fileName,
				Reader: fileReader,
			},
		}
	}

	_, err := s.ChannelMessageSendComplex(id, msg)
	return err
}
