package config

import (
	"gamerpal/internal/config"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// productionLikeRegistry mirrors the real per-module settings so the layout
// invariants below guard the actual panel, without importing internal/commands
// (which would create an import cycle).
func productionLikeRegistry() *config.Registry {
	scamActions := []config.Option{{Value: "log", Label: "Log"}, {Value: "delete", Label: "Delete"}, {Value: "timeout", Label: "Timeout"}}
	translate := []config.Option{{Value: "random", Label: "Random"}, {Value: "caveman", Label: "Caveman"}}
	return config.NewRegistry([]config.Setting{
		{Key: config.KeyModActionLogChannelID, Category: config.CategoryChannels, Label: "Mod Log", Kind: config.KindChannel},
		{Key: config.KeyLogChannelID, Category: config.CategoryChannels, Label: "Log", Kind: config.KindChannel},
		{Key: config.KeyPairingCategoryID, Category: config.CategoryChannels, Label: "Pairing", Kind: config.KindCategory},
		{Key: config.KeyVoiceSyncCategoryID, Category: config.CategoryChannels, Label: "Voice Sync", Kind: config.KindCategory},

		{Key: config.KeyIntroductionsForumChannelID, Category: config.CategoryIntro, Label: "Intro Forum", Kind: config.KindChannel},
		{Key: config.KeyIntroFeedChannelID, Category: config.CategoryIntro, Label: "Intro Feed", Kind: config.KindChannel},
		{Key: config.KeyIntroFeedRateLimitHours, Category: config.CategoryIntro, Label: "Rate Hours", Kind: config.KindInt, Default: 48},
		{Key: config.KeyIntroFeedBoosterRateLimit, Category: config.CategoryIntro, Label: "Booster Hours", Kind: config.KindInt, Default: 0},

		{Key: config.KeyLFGForumChannelID, Category: config.CategoryLFG, Label: "LFG Forum", Kind: config.KindChannel},
		{Key: config.KeyLFGNowPanelChannelID, Category: config.CategoryLFG, Label: "Now Panel", Kind: config.KindChannel},
		{Key: config.KeyLFGNowRoleID, Category: config.CategoryLFG, Label: "Now Role", Kind: config.KindRole},
		{Key: config.KeyLFGNowRoleDuration, Category: config.CategoryLFG, Label: "Now Duration", Kind: config.KindDuration, Default: "2h"},

		{Key: config.KeyNewPalsSystemEnabled, Category: config.CategoryNewPals, Label: "Enabled", Kind: config.KindBool, Default: false},
		{Key: config.KeyNewPalsRoleID, Category: config.CategoryNewPals, Label: "Role", Kind: config.KindRole},
		{Key: config.KeyNewPalsChannelID, Category: config.CategoryNewPals, Label: "Channel", Kind: config.KindChannel},
		{Key: config.KeyNewPalsKeepRoleDuration, Category: config.CategoryNewPals, Label: "Keep", Kind: config.KindDuration, Default: "168h"},
		{Key: config.KeyNewPalsTimeBetweenMsgs, Category: config.CategoryNewPals, Label: "Between", Kind: config.KindDuration, Default: "24h"},

		{Key: config.KeyScamGuardEnabled, Category: config.CategoryScamGuard, Label: "Enabled", Kind: config.KindBool, Default: false},
		{Key: config.KeyScamGuardAction, Category: config.CategoryScamGuard, Label: "Action", Kind: config.KindEnum, EnumOptions: scamActions, Default: "timeout"},
		{Key: config.KeyScamGuardHashThreshold, Category: config.CategoryScamGuard, Label: "Threshold", Kind: config.KindInt, Default: 8},
		{Key: config.KeyScamGuardTimeoutDuration, Category: config.CategoryScamGuard, Label: "Timeout", Kind: config.KindDuration, Default: "168h"},
		{Key: config.KeyScamGuardLogChannelID, Category: config.CategoryScamGuard, Label: "Log", Kind: config.KindChannel},

		{Key: config.KeyCopilotAgentRoleID, Category: config.CategoryAgent, Label: "Role", Kind: config.KindRole},
		{Key: config.KeyCopilotAgentExcludeRoleID, Category: config.CategoryAgent, Label: "Exclude Role", Kind: config.KindRole},
		{Key: config.KeyCopilotAgentReplyAllowlist, Category: config.CategoryAgent, Label: "Allowlist", Kind: config.KindChannelList},
		{Key: config.KeyCopilotAgentModel, Category: config.CategoryAgent, Label: "Model", Kind: config.KindString},

		{Key: config.KeyHelpDeskChannelID, Category: config.CategoryMisc, Label: "Help Desk", Kind: config.KindChannel},
		{Key: config.KeyEventFeedChannelID, Category: config.CategoryMisc, Label: "Event Feed", Kind: config.KindChannel},
		{Key: config.Key1984LogChannelID, Category: config.CategoryMisc, Label: "1984 Log", Kind: config.KindChannel},
		{Key: config.KeyTranslateLanguage, Category: config.CategoryMisc, Label: "Translate", Kind: config.KindEnum, EnumOptions: translate, Default: "random"},
	})
}

func newTestModule() *Module {
	cfg := config.NewMockConfig(map[string]interface{}{
		"gamerpals_server_id": "guild1",
		"super_admins":        []string{"super1"},
	})
	cfg.ApplyRegistry(productionLikeRegistry())
	return &Module{config: cfg}
}

// categoryInner unwraps the Components V2 container the panel renders and
// returns its child components for inspection.
func categoryInner(t *testing.T, data *discordgo.InteractionResponseData) []discordgo.MessageComponent {
	t.Helper()
	if data.Flags&discordgo.MessageFlagsIsComponentsV2 == 0 {
		t.Fatal("expected Components V2 flag on panel response")
	}
	if len(data.Components) != 1 {
		t.Fatalf("expected a single top-level container, got %d components", len(data.Components))
	}
	cont, ok := data.Components[0].(discordgo.Container)
	if !ok {
		t.Fatalf("top-level component is %T, want Container", data.Components[0])
	}
	return cont.Components
}

// TestCategoryLayoutWithinDiscordLimits guards the Components V2 limits: at most
// 40 components per message, at most 5 buttons per action row, and a select
// alone in its row.
func TestCategoryLayoutWithinDiscordLimits(t *testing.T) {
	m := newTestModule()
	for _, cat := range m.config.Registry().Categories() {
		data := m.renderCategory("guild1", cat, "")
		inner := categoryInner(t, data)
		total := 1 // the container itself
		for _, c := range inner {
			total++
			ar, ok := c.(discordgo.ActionsRow)
			if !ok {
				continue
			}
			total += len(ar.Components)
			if len(ar.Components) > 5 {
				t.Errorf("category %q: a row has %d components, want <=5", cat, len(ar.Components))
			}
			for _, cc := range ar.Components {
				if _, ok := cc.(discordgo.SelectMenu); ok && len(ar.Components) != 1 {
					t.Errorf("category %q: a select shares its row with %d components, want 1", cat, len(ar.Components))
				}
			}
		}
		if total == 0 || total > 40 {
			t.Errorf("category %q produced %d components, want 1..40", cat, total)
		}
	}
}

// TestEachSelectHasLabel guards the UX requirement that motivated Components V2:
// every select is immediately preceded by a TextDisplay label, so an admin can
// tell which setting the dropdown changes.
func TestEachSelectHasLabel(t *testing.T) {
	m := newTestModule()
	for _, cat := range m.config.Registry().Categories() {
		inner := categoryInner(t, m.renderCategory("guild1", cat, ""))
		for idx, c := range inner {
			ar, ok := c.(discordgo.ActionsRow)
			if !ok {
				continue
			}
			hasSelect := false
			for _, cc := range ar.Components {
				if _, ok := cc.(discordgo.SelectMenu); ok {
					hasSelect = true
				}
			}
			if !hasSelect {
				continue
			}
			if idx == 0 {
				t.Errorf("category %q: a select row has no preceding label", cat)
				continue
			}
			if _, ok := inner[idx-1].(discordgo.TextDisplay); !ok {
				t.Errorf("category %q: select row preceded by %T, want a TextDisplay label", cat, inner[idx-1])
			}
		}
	}
}

// TestRenderCategoryComponentKinds checks that kinds map to the right Discord
// components: selects for pickers, a toggle button for bools, and an edit
// button when the category has modal-entered fields.
func TestRenderCategoryComponentKinds(t *testing.T) {
	m := newTestModule()

	// Server Channels: all selects, no toggle/edit, plus a back row.
	channels := categoryInner(t, m.renderCategory("guild1", config.CategoryChannels, ""))
	selects := 0
	for _, c := range channels {
		ar, ok := c.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, cc := range ar.Components {
			if _, ok := cc.(discordgo.SelectMenu); ok {
				selects++
			}
		}
	}
	if selects != 4 {
		t.Errorf("Server Channels: got %d selects, want 4", selects)
	}

	// ScamGuard: has a bool (toggle) and modal fields (edit button).
	scam := categoryInner(t, m.renderCategory("guild1", config.CategoryScamGuard, ""))
	var hasToggle, hasEdit bool
	for _, c := range scam {
		ar, ok := c.(discordgo.ActionsRow)
		if !ok {
			continue
		}
		for _, cc := range ar.Components {
			if b, ok := cc.(discordgo.Button); ok {
				switch b.CustomID {
				case toggleID(config.KeyScamGuardEnabled):
					hasToggle = true
				case editID(string(config.CategoryScamGuard)):
					hasEdit = true
				}
			}
		}
	}
	if !hasToggle {
		t.Error("ScamGuard: missing enabled toggle button")
	}
	if !hasEdit {
		t.Error("ScamGuard: missing edit-values button")
	}
}

func TestAgentLockoutWarning(t *testing.T) {
	// allowlist set, no role/exclude -> warning.
	cfg := config.NewMockConfig(map[string]interface{}{
		"gamerpals_server_id":                   "guild1",
		"copilot_agent_reply_channel_allowlist": "123",
	})
	cfg.ApplyRegistry(productionLikeRegistry())
	m := &Module{config: cfg}
	if w := m.agentWarning(cfg.ForGuild("guild1")); w == "" {
		t.Error("expected lockout warning when allowlist set with no role gate")
	}

	// add an inclusion role -> no warning.
	cfg2 := config.NewMockConfig(map[string]interface{}{
		"gamerpals_server_id":                   "guild1",
		"copilot_agent_reply_channel_allowlist": "123",
		"copilot_agent_role_id":                 "role1",
	})
	m2 := &Module{config: cfg2}
	if w := m2.agentWarning(cfg2.ForGuild("guild1")); w != "" {
		t.Errorf("expected no warning with inclusion role set, got %q", w)
	}
}

func TestCanManage(t *testing.T) {
	m := newTestModule()
	cases := []struct {
		name  string
		i     *discordgo.InteractionCreate
		allow bool
	}{
		{
			name:  "super admin without perms",
			i:     memberInteraction("super1", 0),
			allow: true,
		},
		{
			name:  "ban members permission",
			i:     memberInteraction("u2", discordgo.PermissionBanMembers),
			allow: true,
		},
		{
			name:  "administrator permission",
			i:     memberInteraction("u3", discordgo.PermissionAdministrator),
			allow: true,
		},
		{
			name:  "no perms, not super admin",
			i:     memberInteraction("u4", 0),
			allow: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.canManage(tc.i); got != tc.allow {
				t.Errorf("canManage = %v, want %v", got, tc.allow)
			}
		})
	}
}

func memberInteraction(userID string, perms int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Member: &discordgo.Member{
			User:        &discordgo.User{ID: userID},
			Permissions: perms,
		},
	}}
}

func TestFormatValue(t *testing.T) {
	cases := []struct {
		name string
		st   config.Setting
		raw  string
		want string
	}{
		{"channel", config.Setting{Kind: config.KindChannel}, "111", "<#111>"},
		{"category", config.Setting{Kind: config.KindCategory}, "222", "<#222>"},
		{"role", config.Setting{Kind: config.KindRole}, "333", "<@&333>"},
		{"channel list", config.Setting{Kind: config.KindChannelList}, "1,2", "<#1>, <#2>"},
		{"bool on", config.Setting{Kind: config.KindBool}, "true", "✅ On"},
		{"bool off", config.Setting{Kind: config.KindBool}, "false", "❌ Off"},
		{"enum label", config.Setting{Kind: config.KindEnum, EnumOptions: []config.Option{{Value: "a", Label: "Apple"}}}, "a", "Apple"},
		{"int code", config.Setting{Kind: config.KindInt}, "48", "`48`"},
		{"empty", config.Setting{Kind: config.KindChannel}, "", "_not set_"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatValue(tc.st, tc.raw); got != tc.want {
				t.Errorf("formatValue = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSplitCSVAndTruncate(t *testing.T) {
	got := splitCSV(" 1 , ,2,3 ")
	want := []string{"1", "2", "3"}
	if len(got) != len(want) {
		t.Fatalf("splitCSV len = %d, want %d", len(got), len(want))
	}
	for idx := range want {
		if got[idx] != want[idx] {
			t.Errorf("splitCSV[%d] = %q, want %q", idx, got[idx], want[idx])
		}
	}
	if truncate("abcdef", 4) != "abc…" {
		t.Errorf("truncate = %q, want %q", truncate("abcdef", 4), "abc…")
	}
	if truncate("abc", 10) != "abc" {
		t.Errorf("truncate should leave short strings unchanged")
	}
}
