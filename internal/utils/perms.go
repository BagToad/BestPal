package utils

import (
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

// HasRoleByName checks if the user has a specific role by name (the text descriptor)
func HasRoleByName(s *discordgo.Session, i *discordgo.InteractionCreate, roleName string) bool {
	for _, role := range i.Member.Roles {
		role, err := s.State.Role(i.GuildID, role)
		if err != nil {
			continue
		}
		if role.Name == roleName {
			return true
		}
	}
	return false
}
