package config

import (
	"os"
	"strings"
	"time"
)

func (c *Config) GetBotToken() string {
	return c.v.GetString("bot_token")
}

func (c *Config) GetIGDBClientID() string {
	return c.v.GetString("igdb_client_id")
}

func (c *Config) GetIGDBClientSecret() string {
	return c.v.GetString("igdb_client_secret")
}

func (c *Config) GetIGDBClientToken() string {
	return c.v.GetString("igdb_client_token")
}

func (c *Config) GetCryptoSalt() string {
	return c.v.GetString("crypto_salt")
}

func (c *Config) GetGitHubModelsToken() string {
	return c.v.GetString("github_models_token")
}

// GetCopilotAgentRoleID returns the role ID that gates inclusion access to
// the LLM tool-calling agent. When set, only members of this role can use the
// agent and GetCopilotAgentExcludeRoleID is ignored.
func (c *Config) GetCopilotAgentRoleID() string {
	return c.v.GetString("copilot_agent_role_id")
}

// GetCopilotAgentExcludeRoleID returns the role ID that gates exclusion access
// to the LLM tool-calling agent. Only honored when GetCopilotAgentRoleID is
// empty: when set, every guild member can use the agent except those with this
// role. When both are unset, the agent is disabled for everyone.
func (c *Config) GetCopilotAgentExcludeRoleID() string {
	return c.v.GetString("copilot_agent_exclude_role_id")
}

// GetCopilotAgentReplyChannelAllowlist returns the list of channel IDs in
// which the LLM tool-calling agent is allowed to reply. When empty, no
// channel restriction is applied (the role gate alone decides). When set,
// only @mentions posted in one of these channels trigger a reply, except
// for super admins and members of the inclusion role (GetCopilotAgentRoleID)
// who bypass the channel check.
func (c *Config) GetCopilotAgentReplyChannelAllowlist() []string {
	raw := c.v.GetStringSlice("copilot_agent_reply_channel_allowlist")

	// Mirror GetSuperAdmins: viper returns a single-element slice with the
	// raw CSV when the value comes from the env var.
	if _, fromEnv := os.LookupEnv("GAMERPAL_COPILOT_AGENT_REPLY_CHANNEL_ALLOWLIST"); fromEnv && len(raw) == 1 {
		raw = strings.Split(raw[0], ",")
	}

	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// GetCopilotAgentModel returns the model identifier used for the LLM tool-calling
// agent. Defaults to "gpt-5.5" when unset.
func (c *Config) GetCopilotAgentModel() string {
	v := c.v.GetString("copilot_agent_model")
	if v == "" {
		return "gpt-5.5"
	}
	return v
}

// GetCopilotAgentCLIPath returns the path to the Copilot CLI binary used by the
// LLM tool-calling agent. When empty, the SDK default ("copilot" on PATH) is used.
func (c *Config) GetCopilotAgentCLIPath() string {
	return c.v.GetString("copilot_agent_cli_path")
}

func (c *Config) GetGamerPalsServerID() string {
	return c.v.GetString("gamerpals_server_id")
}

func (c *Config) GetGamerPalsModActionLogChannelID() string {
	return c.v.GetString("gamerpals_mod_action_log_channel_id")
}

func (c *Config) GetGamerpalsLogChannelID() string {
	return c.v.GetString("gamerpals_log_channel_id")
}

func (c *Config) GetGamerPalsPairingCategoryID() string {
	return c.v.GetString("gamerpals_pairing_category_id")
}

func (c *Config) GetGamerPalsIntroductionsForumChannelID() string {
	return c.v.GetString("gamerpals_introductions_forum_channel_id")
}

func (c *Config) GetGamerPalsHelpDeskChannelID() string {
	return c.v.GetString("gamerpals_help_desk_channel_id")
}

func (c *Config) GetGamerPalsLFGForumChannelID() string {
	return c.v.GetString("gamerpals_lfg_forum_channel_id")
}

func (c *Config) GetGamerPalsVoiceSyncCategoryID() string {
	return c.v.GetString("gamerpals_voice_sync_category_id")
}

// GetGamerPals1984LogChannelID returns the channel ID where the 1984 module
// posts message activity logs (creates, edits, deletes, reactions).
func (c *Config) GetGamerPals1984LogChannelID() string {
	return c.v.GetString("gamerpals_1984_log_channel_id")
}

// LFG Looking NOW panel channel ID (persisted so panel survives restarts)
func (c *Config) GetLFGNowPanelChannelID() string {
	return c.v.GetString("gamerpals_lfg_now_panel_channel_id")
}

// LFG Looking NOW role
func (c *Config) GetLFGNowRoleID() string {
	return c.v.GetString("lfg_now_role_id")
}

func (c *Config) GetLFGNowRoleDuration() time.Duration {
	return c.v.GetDuration("lfg_now_role_duration")
}

// New Pals systems
// -----
func (c *Config) GetNewPalsSystemEnabled() bool {
	return c.v.GetBool("new_pals_system_enabled")
}

func (c *Config) GetNewPalsRoleID() string {
	return c.v.GetString("new_pals_role_id")
}

func (c *Config) GetNewPalsChannelID() string {
	return c.v.GetString("new_pals_channel_id")
}
func (c *Config) GetNewPalsKeepRoleDuration() time.Duration {
	return c.v.GetDuration("new_pals_keep_role_duration")
}

func (c *Config) GetNewPalsTimeBetweenWelcomeMessages() time.Duration {
	return c.v.GetDuration("new_pals_time_between_welcome_messages")
}

func (c *Config) GetSuperAdmins() []string {
	superAdmins := c.v.GetStringSlice("super_admins")

	// When the value comes from an env var (GAMERPAL_SUPER_ADMINS=id1,id2,id3),
	// viper returns a single-element slice containing the raw CSV string and
	// we have to split it ourselves. We gate this on the env var actually
	// being set so a single YAML element that happens to contain a literal
	// comma is not silently turned into multiple entries.
	if _, fromEnv := os.LookupEnv("GAMERPAL_SUPER_ADMINS"); fromEnv && len(superAdmins) == 1 {
		superAdmins = strings.Split(superAdmins[0], ",")
	}

	// Trim every element regardless of source. This normalizes " id " from
	// YAML and "id1, id2" from env to the same shape, and drops empty
	// entries left over from trailing/duplicate commas.
	out := make([]string, 0, len(superAdmins))
	for _, s := range superAdmins {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (c *Config) GetDatabasePath() string {
	dbPath := c.v.GetString("database_path")
	return dbPath
}

func (c *Config) GetLogDir() string {
	return c.v.GetString("log_dir")
}

// GetDisableFileLogging returns true when the bot should only log to stderr
// and skip writing/rotating timestamped log files. Useful in containerized
// deployments where stdout/stderr is captured by the platform.
func (c *Config) GetDisableFileLogging() bool {
	return c.v.GetBool("disable_file_logging")
}

func (c *Config) Set(key string, value interface{}) {
	c.v.Set(key, value)
	if err := c.v.WriteConfig(); err != nil {
		c.Logger.Warnf("failed to write config for key %s: %v", key, err)
	}
}

// GetString returns the string value for a given config key
func (c *Config) GetString(key string) string {
	return c.v.GetString(key)
}

// Event Feed configuration
// -----

// GetEventFeedChannelID returns the channel ID where new scheduled events are forwarded
func (c *Config) GetEventFeedChannelID() string {
	return c.v.GetString("event_feed_channel_id")
}

// Introduction Feed configuration
// -----

// GetIntroFeedChannelID returns the channel ID where intro posts are forwarded
func (c *Config) GetIntroFeedChannelID() string {
	return c.v.GetString("intro_feed_channel_id")
}

// GetIntroFeedRateLimitHours returns the number of hours between allowed feed posts per user (default 48)
func (c *Config) GetIntroFeedRateLimitHours() int {
	hours := c.v.GetInt("intro_feed_rate_limit_hours")
	if hours <= 0 {
		return 48 // default to 48 hours
	}
	return hours
}

// GetIntroFeedBoosterRateLimitHours returns the number of hours between allowed feed posts for
// server (Nitro) boosters. A value <= 0 means it is not configured, in which case boosters use
// the standard rate limit (GetIntroFeedRateLimitHours).
func (c *Config) GetIntroFeedBoosterRateLimitHours() int {
	return c.v.GetInt("intro_feed_booster_rate_limit_hours")
}

// Translate language configuration
// -----

// GetTranslateLanguage returns the configured translate language style.
// Valid values: "caveman", "gen_alpha", "old_man", "80s", "high_society", "random"
func (c *Config) GetTranslateLanguage() string {
	lang := c.v.GetString("translate_language")
	if lang == "" {
		return "random"
	}
	return lang
}

// ScamGuard (anti-scam image detection)
// -----

// GetScamGuardEnabled returns the master switch for the scamguard module. When
// false (default), no image hashing or enforcement happens.
func (c *Config) GetScamGuardEnabled() bool {
	return c.v.GetBool("scamguard_enabled")
}

// GetScamGuardHashThreshold returns the maximum Hamming distance between a
// 64-bit perceptual hash and a known-bad hash for the two to be considered a
// match. 0 means an exact match; higher is looser. Defaults to 8 and is capped
// at 16: with a 64-bit hash, larger thresholds match nearly anything and would
// mass-action innocent users, so values above 16 are treated as 16.
func (c *Config) GetScamGuardHashThreshold() int {
	if !c.v.IsSet("scamguard_hash_threshold") {
		return 8
	}
	t := c.v.GetInt("scamguard_hash_threshold")
	if t < 0 {
		return 8
	}
	if t > 16 {
		return 16
	}
	return t
}

// GetScamGuardAction returns the action taken on a hash match: "log" (log
// only), "delete" (delete the message and log), or "timeout" (delete, time the
// author out, and log). Defaults to "timeout".
func (c *Config) GetScamGuardAction() string {
	switch a := strings.ToLower(strings.TrimSpace(c.v.GetString("scamguard_action"))); a {
	case "log", "delete", "timeout":
		return a
	default:
		return "timeout"
	}
}

// GetScamGuardTimeoutDuration returns how long a matched author is timed out
// when the action is "timeout". Defaults to 168h (7 days). Discord caps
// timeouts at 28 days; callers should clamp if needed.
func (c *Config) GetScamGuardTimeoutDuration() time.Duration {
	d := c.v.GetDuration("scamguard_timeout_duration")
	if d <= 0 {
		return 168 * time.Hour
	}
	return d
}

// GetScamGuardLogChannelID returns the channel where scamguard actions are
// logged. Falls back to the mod action log channel when unset.
func (c *Config) GetScamGuardLogChannelID() string {
	if id := c.v.GetString("scamguard_log_channel_id"); id != "" {
		return id
	}
	return c.GetGamerPalsModActionLogChannelID()
}

// GetScamGuardSeedHashesPath returns an optional path to an external seed file
// of known-bad hashes loaded at startup in addition to the embedded seed.
func (c *Config) GetScamGuardSeedHashesPath() string {
	return c.v.GetString("scamguard_seed_hashes_path")
}
