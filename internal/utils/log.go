package utils

import (
	"errors"
	"fmt"
	"gamerpal/internal/config"
	"io"
	"os"
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

func LogToChannelWithFile(cfg *config.Config, s *discordgo.Session, fileContent string) error {
	// Create a file to upload based on string
	file, err := os.CreateTemp("", "log-*.txt")
	if err != nil {
		return err
	}
	defer func() {
		if err := os.Remove(file.Name()); err != nil {
			cfg.Logger.Errorf("failed to remove temp log file: %v", err)
		}
	}()

	if _, err := file.WriteString(fileContent); err != nil {
		return err
	}

	// Rewind so the subsequent read for upload starts at beginning; otherwise empty file is sent.
	if _, err := file.Seek(0, 0); err != nil {
		return err
	}

	if id := cfg.GetGamerpalsLogChannelID(); id != "" {
		_, err = s.ChannelFileSend(id, "log.txt", file)
		if err != nil {
			return fmt.Errorf("failed to send log file: %v", err)
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

	const maxLen = 1900
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
