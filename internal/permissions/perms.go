// Package permissions contains Discord permission and authorization checks.
package permissions

import (
	"gamerpal/internal/config"
	"slices"

	"github.com/bwmarrin/discordgo"
)

type AdminPermissionsOptions struct {
	Session   *discordgo.Session
	UserID    string
	ChannelID string
}

// HasAdminPermissions checks if the user has administrator permissions
func HasAdminPermissions(opts AdminPermissionsOptions) bool {
	if opts.Session == nil || opts.UserID == "" || opts.ChannelID == "" {
		return false
	}

	// Get the member's permissions
	permissions, err := opts.Session.UserChannelPermissions(opts.UserID, opts.ChannelID)
	if err != nil {
		return false
	}

	// Check for administrator permission
	return permissions&discordgo.PermissionAdministrator != 0
}

type BanPermissionsOptions struct {
	Interaction *discordgo.InteractionCreate
}

// HasBanPermissions reports whether the interaction user holds guild-level ban
// permissions. Ban Members and Administrator are guild-wide bits; using
// i.Member.Permissions (supplied by Discord in the interaction payload) avoids
// a channel-permission lookup that can be masked by channel overwrites and
// diverge from actual moderation authority.
func HasBanPermissions(opts BanPermissionsOptions) bool {
	i := opts.Interaction
	if i == nil || i.Member == nil {
		return false
	}
	const banBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator
	return i.Member.Permissions&banBits != 0
}

type CreateBanOptions struct {
	Session    *discordgo.Session
	GuildID    string
	UserID     string
	Reason     string
	DeleteDays int
}

// CreateBan is the shared Discord ban operation used by moderation features.
func CreateBan(opts CreateBanOptions) error {
	if opts.Session == nil || opts.GuildID == "" || opts.UserID == "" {
		return discordgo.ErrNilState
	}
	return opts.Session.GuildBanCreateWithReason(opts.GuildID, opts.UserID, opts.Reason, opts.DeleteDays)
}

type SuperAdminOptions struct {
	UserID string
	Config *config.Config
}

func IsSuperAdmin(opts SuperAdminOptions) bool {
	if opts.Config == nil {
		return false
	}

	superAdmins := opts.Config.GetSuperAdmins()
	return slices.Contains(superAdmins, opts.UserID)
}
