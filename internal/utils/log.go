package utils

import (
	"errors"
	"gamerpal/internal/config"
	"time"

	"github.com/bwmarrin/discordgo"
)

func LogToChannel(cfg *config.Config, s *discordgo.Session, m string) error {
	logEmbed := &discordgo.MessageEmbed{
		Title:       "Scheduler Message",
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
