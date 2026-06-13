package config

// This file defines the self-registering settings system. Modules declare the
// operational settings they own via a ConfigSettings() method; those settings
// are collected into a Registry that drives the Discord config panel, per-guild
// persistence, and validation. The descriptor is UI-agnostic (a Kind enum, not
// discordgo types) so this package keeps zero dependency on discordgo.

// Per-guild config keys. These are the keys an admin can override per guild via
// the config panel. Both the typed getters on GuildConfig and the owning
// modules' descriptors reference these constants, so the key is defined once.
const (
	KeyModActionLogChannelID = "gamerpals_mod_action_log_channel_id"
	KeyLogChannelID          = "gamerpals_log_channel_id"
	KeyPairingCategoryID     = "gamerpals_pairing_category_id"
	KeyVoiceSyncCategoryID   = "gamerpals_voice_sync_category_id"
	KeyHelpDeskChannelID     = "gamerpals_help_desk_channel_id"
	KeyEventFeedChannelID    = "event_feed_channel_id"

	KeyIntroductionsForumChannelID = "gamerpals_introductions_forum_channel_id"
	KeyIntroFeedChannelID          = "intro_feed_channel_id"
	KeyIntroFeedRateLimitHours     = "intro_feed_rate_limit_hours"
	KeyIntroFeedBoosterRateLimit   = "intro_feed_booster_rate_limit_hours"

	KeyLFGForumChannelID    = "gamerpals_lfg_forum_channel_id"
	KeyLFGNowPanelChannelID = "gamerpals_lfg_now_panel_channel_id"
	KeyLFGNowRoleID         = "lfg_now_role_id"
	KeyLFGNowRoleDuration   = "lfg_now_role_duration"

	KeyNewPalsSystemEnabled    = "new_pals_system_enabled"
	KeyNewPalsRoleID           = "new_pals_role_id"
	KeyNewPalsChannelID        = "new_pals_channel_id"
	KeyNewPalsKeepRoleDuration = "new_pals_keep_role_duration"
	KeyNewPalsTimeBetweenMsgs  = "new_pals_time_between_welcome_messages"

	Key1984LogChannelID = "gamerpals_1984_log_channel_id"

	KeyTranslateLanguage = "translate_language"

	KeyScamGuardEnabled         = "scamguard_enabled"
	KeyScamGuardHashThreshold   = "scamguard_hash_threshold"
	KeyScamGuardAction          = "scamguard_action"
	KeyScamGuardTimeoutDuration = "scamguard_timeout_duration"
	KeyScamGuardLogChannelID    = "scamguard_log_channel_id"

	KeyCopilotAgentRoleID         = "copilot_agent_role_id"
	KeyCopilotAgentExcludeRoleID  = "copilot_agent_exclude_role_id"
	KeyCopilotAgentReplyAllowlist = "copilot_agent_reply_channel_allowlist"
	KeyCopilotAgentModel          = "copilot_agent_model"
)

// Kind is the value type of a setting. The config panel maps each Kind onto the
// appropriate Discord component; this package stays UI-agnostic.
type Kind string

const (
	KindChannel     Kind = "channel"      // a single text/forum channel ID
	KindCategory    Kind = "category"     // a single category channel ID
	KindRole        Kind = "role"         // a single role ID
	KindChannelList Kind = "channel_list" // a CSV list of channel IDs
	KindBool        Kind = "bool"         // a toggle
	KindEnum        Kind = "enum"         // one of EnumOptions
	KindInt         Kind = "int"          // an integer entered via modal
	KindDuration    Kind = "duration"     // a Go duration (e.g. "48h") via modal
	KindString      Kind = "string"       // free text via modal
)

// Category groups settings into panel sections. It is a shared vocabulary:
// settings from different owning modules can share a category, and a category
// is independent of which module declares a given setting.
type Category string

const (
	CategoryChannels  Category = "Server Channels"
	CategoryIntro     Category = "Introductions"
	CategoryLFG       Category = "Looking for Game"
	CategoryNewPals   Category = "New Pals"
	CategoryScamGuard Category = "ScamGuard"
	CategoryAgent     Category = "Agent"
	CategoryMisc      Category = "Moderation & Misc"
)

// categoryOrder is the canonical display order of categories in the panel.
var categoryOrder = []Category{
	CategoryChannels,
	CategoryIntro,
	CategoryLFG,
	CategoryNewPals,
	CategoryScamGuard,
	CategoryAgent,
	CategoryMisc,
}

// Option is a single choice for a KindEnum setting.
type Option struct {
	Value string
	Label string
}

// Setting is a UI-agnostic descriptor for one per-guild config value. Modules
// declare the settings they own; the panel renders them and the store persists
// them. Default and Validate keep the value's semantics in one place, alongside
// the typed getter on GuildConfig.
type Setting struct {
	Key         string
	Category    Category
	Label       string
	Description string
	Kind        Kind
	Default     any
	EnumOptions []Option           // populated for KindEnum
	Validate    func(string) error // optional; nil means validate by Kind
}

// ConfigProvider is implemented by any component that owns config settings
// (command modules, the agent, the core provider). It is intentionally kept out
// of the core CommandModule contract, mirroring the agentToolProvider pattern,
// so a component opts in simply by declaring this method.
type ConfigProvider interface {
	ConfigSettings() []Setting
}

// Registry is the collected set of all declared settings. It is the single
// source of truth the config panel renders from.
type Registry struct {
	settings []Setting
	byKey    map[string]Setting
}

// NewRegistry builds a Registry from settings already de-duplicated by the
// collector. Settings are kept in the order provided.
func NewRegistry(settings []Setting) *Registry {
	byKey := make(map[string]Setting, len(settings))
	for _, s := range settings {
		byKey[s.Key] = s
	}
	return &Registry{settings: settings, byKey: byKey}
}

// All returns every registered setting.
func (r *Registry) All() []Setting {
	if r == nil {
		return nil
	}
	return r.settings
}

// Get returns the setting for a key.
func (r *Registry) Get(key string) (Setting, bool) {
	if r == nil {
		return Setting{}, false
	}
	s, ok := r.byKey[key]
	return s, ok
}

// Categories returns the categories that have at least one setting, in
// canonical display order. Any category not in the canonical list is appended
// afterwards in first-seen order.
func (r *Registry) Categories() []Category {
	if r == nil {
		return nil
	}
	present := make(map[Category]bool)
	for _, s := range r.settings {
		present[s.Category] = true
	}
	var out []Category
	for _, c := range categoryOrder {
		if present[c] {
			out = append(out, c)
			delete(present, c)
		}
	}
	for _, s := range r.settings {
		if present[s.Category] {
			out = append(out, s.Category)
			delete(present, s.Category)
		}
	}
	return out
}

// ByCategory returns the settings in a category, preserving registration order.
func (r *Registry) ByCategory(c Category) []Setting {
	if r == nil {
		return nil
	}
	var out []Setting
	for _, s := range r.settings {
		if s.Category == c {
			out = append(out, s)
		}
	}
	return out
}

// GuildStore is the persistence contract the config package needs for per-guild
// overrides. database.DB implements it. Defined here (rather than importing
// database) so the config package stays free of a database dependency and no
// import cycle is created.
type GuildStore interface {
	// AllGuildConfig returns every stored override for a guild as key->value.
	AllGuildConfig(guildID string) (map[string]string, error)
	// SetGuildConfigValue upserts an override, recording the editor's user ID.
	SetGuildConfigValue(guildID, key, value, updatedBy string) error
	// DeleteGuildConfigValue removes an override, reverting to env/default.
	DeleteGuildConfigValue(guildID, key string) error
}
