package events

import (
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

func OnGuildMemberAdd(s *discordgo.Session, m *discordgo.GuildMemberAdd, cfg *config.Config) {
	if cfg.GetNewPalsSystemEnabled() == false {
		return
	}

	// When a new member joins, we immediately give them the `new-pals` role
	roleID := cfg.GetNewPalsRoleID()
	if roleID == "" {
		cfg.Logger.Error("No role ID found for 'new-pals'")
		return
	}

	err := s.GuildMemberRoleAdd(m.GuildID, m.User.ID, roleID)
	if err != nil {
		cfg.Logger.Error("Failed to add role to new member:", err)
		return
	}

	cfg.Logger.Infof("Added 'new-pals (%s)' role to new member %s (%s)", roleID, m.User.Username, m.User.ID)
}
