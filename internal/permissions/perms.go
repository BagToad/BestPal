// Package permissions contains Discord permission and authorization checks.
package permissions

import (
	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

type AdminPermissionsOptions struct {
	Session   *discordgo.Session
	UserID    string
	ChannelID string
}

// HasAdminPermissions reports whether a user has Administrator in a channel.
func HasAdminPermissions(opts AdminPermissionsOptions) bool {
	permissions, err := resolveChannelPermissions(opts)
	return err == nil && permissions&discordgo.PermissionAdministrator != 0
}

// HasModeratorPermissions reports whether a user has moderator-level authority.
func HasModeratorPermissions(opts AdminPermissionsOptions) (bool, error) {
	permissions, err := resolveChannelPermissions(opts)
	if err != nil {
		return false, err
	}
	return permissions&(discordgo.PermissionManageMessages|discordgo.PermissionAdministrator) != 0, nil
}

func resolveChannelPermissions(opts AdminPermissionsOptions) (int64, error) {
	if opts.Session == nil || opts.UserID == "" || opts.ChannelID == "" {
		return 0, discordgo.ErrNilState
	}
	return opts.Session.UserChannelPermissions(opts.UserID, opts.ChannelID)
}

type BanPermissionsOptions struct {
	Interaction *discordgo.InteractionCreate
}

// HasBanPermissions reports whether an interaction user can ban members.
func HasBanPermissions(opts BanPermissionsOptions) bool {
	if opts.Interaction == nil || opts.Interaction.Member == nil {
		return false
	}
	const banBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator
	return opts.Interaction.Member.Permissions&banBits != 0
}

type CreateBanOptions struct {
	Session    *discordgo.Session
	GuildID    string
	UserID     string
	Reason     string
	DeleteDays int
}

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
	if opts.Config == nil || opts.UserID == "" {
		return false
	}
	for _, id := range opts.Config.GetSuperAdmins() {
		if id == opts.UserID {
			return true
		}
	}
	return false
}
