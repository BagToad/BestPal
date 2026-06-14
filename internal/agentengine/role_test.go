package agentengine

import (
	"testing"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

func msgInChannel(guildID, channelID, authorID string, roles []string) *discordgo.MessageCreate {
	mc := &discordgo.MessageCreate{Message: &discordgo.Message{GuildID: guildID, ChannelID: channelID}}
	if roles != nil {
		mc.Member = &discordgo.Member{Roles: roles}
	}
	if authorID != "" {
		mc.Author = &discordgo.User{ID: authorID}
	}
	return mc
}

// TestUserHasAgentRole_TruthTable is the canonical end-to-end enforcement of
// the agent admission truth table documented for userHasAgentRole. Each row
// names a real-world scenario combining guild/super-admin/role/channel state.
// If a row here changes, the docstring on userHasAgentRole must change too.
func TestUserHasAgentRole_TruthTable(t *testing.T) {
	const (
		guildID       = "guild-1"
		otherGuild    = "guild-other"
		includeRoleID = "role-include"
		excludeRoleID = "role-exclude"
		allowedCh     = "channel-allowed"
		otherCh       = "channel-other"
		regularUser   = "user-regular"
		superAdmin    = "user-super"
	)

	type cfgKV map[string]any

	cases := []struct {
		name    string
		cfg     cfgKV
		message *discordgo.MessageCreate
		want    bool
	}{
		// Guild gate is absolute. Super admin cannot bypass it.
		{
			"guild gate: wrong guild rejects super admin",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_exclude_role_id": excludeRoleID,
				"super_admins":                  []string{superAdmin},
			},
			msgInChannel(otherGuild, allowedCh, superAdmin, []string{}),
			false,
		},
		{
			"guild gate: unconfigured guild rejects super admin",
			cfgKV{
				"super_admins": []string{superAdmin},
			},
			msgInChannel(guildID, allowedCh, superAdmin, []string{}),
			false,
		},

		// Super admin bypasses everything except the guild gate.
		{
			"super admin: bypasses missing role config",
			cfgKV{
				"gamerpals_server_id": guildID,
				"super_admins":        []string{superAdmin},
			},
			msgInChannel(guildID, otherCh, superAdmin, nil),
			true,
		},
		{
			"super admin: bypasses exclusion role and channel allowlist",
			cfgKV{
				"gamerpals_server_id":                   guildID,
				"copilot_agent_exclude_role_id":         excludeRoleID,
				"copilot_agent_reply_channel_allowlist": []string{allowedCh},
				"super_admins":                          []string{superAdmin},
			},
			msgInChannel(guildID, otherCh, superAdmin, []string{excludeRoleID}),
			true,
		},
		{
			"super admin: bypasses missing include role",
			cfgKV{
				"gamerpals_server_id":   guildID,
				"copilot_agent_role_id": includeRoleID,
				"super_admins":          []string{superAdmin},
			},
			msgInChannel(guildID, otherCh, superAdmin, []string{}),
			true,
		},

		// Role gate: nobody passes when nothing is configured.
		{
			"role gate: both roles unset rejects non-admin",
			cfgKV{
				"gamerpals_server_id": guildID,
			},
			msgInChannel(guildID, allowedCh, regularUser, []string{}),
			false,
		},

		// Inclusion mode (allowlist empty).
		{
			"include mode, allowlist empty: include role accepted anywhere",
			cfgKV{
				"gamerpals_server_id":   guildID,
				"copilot_agent_role_id": includeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{includeRoleID}),
			true,
		},
		{
			"include mode, allowlist empty: missing role rejected",
			cfgKV{
				"gamerpals_server_id":   guildID,
				"copilot_agent_role_id": includeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{}),
			false,
		},

		// Inclusion mode (allowlist set): include role bypasses channel check.
		{
			"include mode, allowlist set: include role bypasses channel",
			cfgKV{
				"gamerpals_server_id":                   guildID,
				"copilot_agent_role_id":                 includeRoleID,
				"copilot_agent_reply_channel_allowlist": []string{allowedCh},
			},
			msgInChannel(guildID, otherCh, regularUser, []string{includeRoleID}),
			true,
		},
		{
			"include mode, allowlist set: missing role rejected even in allowed channel",
			cfgKV{
				"gamerpals_server_id":                   guildID,
				"copilot_agent_role_id":                 includeRoleID,
				"copilot_agent_reply_channel_allowlist": []string{allowedCh},
			},
			msgInChannel(guildID, allowedCh, regularUser, []string{}),
			false,
		},

		// Both roles configured: inclusion wins, exclusion is ignored.
		{
			"both roles set: include role accepted (exclusion ignored)",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_role_id":         includeRoleID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{includeRoleID}),
			true,
		},
		{
			"both roles set: include and exclude roles together still accepted",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_role_id":         includeRoleID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{includeRoleID, excludeRoleID}),
			true,
		},
		{
			"both roles set: only exclude role rejected",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_role_id":         includeRoleID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{excludeRoleID}),
			false,
		},
		{
			"both roles set: neither role rejected",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_role_id":         includeRoleID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{"other"}),
			false,
		},

		// Exclusion mode (allowlist empty).
		{
			"exclude mode, allowlist empty: clean user passes anywhere",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, otherCh, regularUser, []string{}),
			true,
		},
		{
			"exclude mode, allowlist empty: excluded user rejected",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, allowedCh, regularUser, []string{excludeRoleID}),
			false,
		},

		// Exclusion mode (allowlist set): channel gate enforced for non-admins.
		{
			"exclude mode, allowlist set: clean user in allowed channel passes",
			cfgKV{
				"gamerpals_server_id":                   guildID,
				"copilot_agent_exclude_role_id":         excludeRoleID,
				"copilot_agent_reply_channel_allowlist": []string{allowedCh},
			},
			msgInChannel(guildID, allowedCh, regularUser, []string{}),
			true,
		},
		{
			"exclude mode, allowlist set: clean user in other channel rejected",
			cfgKV{
				"gamerpals_server_id":                   guildID,
				"copilot_agent_exclude_role_id":         excludeRoleID,
				"copilot_agent_reply_channel_allowlist": []string{allowedCh},
			},
			msgInChannel(guildID, otherCh, regularUser, []string{}),
			false,
		},
		{
			"exclude mode, allowlist set: excluded user rejected even in allowed channel",
			cfgKV{
				"gamerpals_server_id":                   guildID,
				"copilot_agent_exclude_role_id":         excludeRoleID,
				"copilot_agent_reply_channel_allowlist": []string{allowedCh},
			},
			msgInChannel(guildID, allowedCh, regularUser, []string{excludeRoleID}),
			false,
		},

		// Nil-member safety for non-admins.
		{
			"nil member: non-admin rejected",
			cfgKV{
				"gamerpals_server_id":           guildID,
				"copilot_agent_exclude_role_id": excludeRoleID,
			},
			msgInChannel(guildID, allowedCh, regularUser, nil),
			false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := userHasAgentRole(config.NewMockConfig(tc.cfg), tc.message); got != tc.want {
				t.Fatalf("userHasAgentRole = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUserHasAgentRole_NilSafe(t *testing.T) {
	if userHasAgentRole(nil, nil) {
		t.Fatal("nil cfg + nil message should be false")
	}
	cfg := config.NewMockConfig(map[string]any{"copilot_agent_role_id": "r", "gamerpals_server_id": "g"})
	if userHasAgentRole(cfg, nil) {
		t.Fatal("nil message should be false")
	}
}
