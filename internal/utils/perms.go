package utils

import (
	"gamerpal/internal/config"
	"slices"

	"github.com/bwmarrin/discordgo"
)

// CreateBan is the shared Discord ban operation used by moderation features.
func CreateBan(s *discordgo.Session, guildID, userID, reason string, days int) error {
	return s.GuildBanCreateWithReason(guildID, userID, reason, days)
}

// HasBanPermissions checks whether the interaction user can ban members in the
// current channel. Administrators are included because Discord treats that
// permission as implying Ban Members.
func HasBanPermissions(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if s == nil || i == nil || i.Member == nil || i.Member.User == nil {
		return false
	}
	permissions, err := s.UserChannelPermissions(i.Member.User.ID, i.ChannelID)
	if err != nil {
		return false
	}
	return permissions&(discordgo.PermissionBanMembers|discordgo.PermissionAdministrator) != 0
}

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
	return slices.Contains(superAdmins, ID)
}
