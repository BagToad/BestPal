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

// HasBanPermissions reports whether the interaction user holds guild-level ban
// permissions. Ban Members and Administrator are guild-wide bits; using
// i.Member.Permissions (supplied by Discord in the interaction payload) avoids
// a channel-permission lookup that can be masked by channel overwrites and
// diverge from actual moderation authority.
func HasBanPermissions(i *discordgo.InteractionCreate) bool {
	if i == nil || i.Member == nil {
		return false
	}
	const banBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator
	return i.Member.Permissions&banBits != 0
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
