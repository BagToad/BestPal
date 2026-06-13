package utils

import (
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// HasAdminPermissions checks if the user has administrator permissions
func HasAdminPermissions(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	// Get the member's permissions
	permissions, err := s.UserChannelPermissions(i.Member.User.ID, i.ChannelID)
	if err != nil {
		return false
	}

	// Check for administrator permission
	return permissions&discordgo.PermissionAdministrator != 0
}

func IsSuperAdmin(ID string, config *config.Config) bool {
	if config == nil {
		return false
	}

	superAdmins := config.GetSuperAdmins()
	for _, adminID := range superAdmins {
		if adminID == ID {
			return true
		}
	}
	return false
}
