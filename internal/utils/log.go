package utils

import (
	"errors"
	"fmt"
	"gamerpal/internal/config"
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
			cfg.Logger.Error("failed to remove temp log file: %v", err)
		}
	}()

	if _, err := file.WriteString(fileContent); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}

	// Send the usual embed indicating it's a bestpal log.
	embed := &discordgo.MessageEmbed{
		Title:       "Best Pal Log",
		Description: "Log file attached.",
		Color:       Colors.Info(),
		Timestamp:   time.Now().Format(time.RFC3339),
	}

	if id := cfg.GetGamerpalsLogChannelID(); id != "" {
		_, err := s.ChannelMessageSendEmbed(id, embed)
		if err != nil {
			return fmt.Errorf("failed to send log embed: %v", err)
		}

		_, err = s.ChannelFileSend(id, "log.txt", file)
		if err != nil {
			return fmt.Errorf("failed to send log file: %v", err)
		}

	} else {
		return errors.New("unable to log to channel: gamerpals_log_channel_id is not set")
	}

	return nil
}
