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

// GetCopilotAgentCLIPath returns the path to the Copilot CLI binary used by the
// LLM tool-calling agent. When empty, the SDK default ("copilot" on PATH) is
// used. Bootstrap/infra setting: env-only, never per-guild.
func (c *Config) GetCopilotAgentCLIPath() string {
	return c.v.GetString("copilot_agent_cli_path")
}

// GetGamerPalsServerID returns the operating guild ID. This is the per-guild
// key itself, so it is global and env-only (never overridable per guild).
func (c *Config) GetGamerPalsServerID() string {
	return c.v.GetString("gamerpals_server_id")
}

// Per-guild settings (delegators)
// -----
//
// The real logic for these lives on *GuildConfig (see guild_config.go), which
// resolves a per-guild override before falling back to env/default. These
// delegators preserve the existing *Config API: a plain cfg.GetX() reads the
// primary (operating) guild, so existing call sites need no changes, while the
// config panel reads/writes a specific guild via cfg.ForGuild(id).

// GetCopilotAgentRoleID returns the inclusion role gating the LLM agent. When
// set, only members of this role can use the agent and the exclude role is
// ignored.
func (c *Config) GetCopilotAgentRoleID() string {
	return c.PrimaryGuild().GetCopilotAgentRoleID()
}

// GetCopilotAgentExcludeRoleID returns the exclusion role gating the LLM agent.
// Only honored when the inclusion role is empty.
func (c *Config) GetCopilotAgentExcludeRoleID() string {
	return c.PrimaryGuild().GetCopilotAgentExcludeRoleID()
}

// GetCopilotAgentReplyChannelAllowlist returns the channel IDs in which the LLM
// agent may reply. Empty means no channel restriction.
func (c *Config) GetCopilotAgentReplyChannelAllowlist() []string {
	return c.PrimaryGuild().GetCopilotAgentReplyChannelAllowlist()
}

// GetCopilotAgentModel returns the agent model identifier (default "gpt-5.5").
func (c *Config) GetCopilotAgentModel() string {
	return c.PrimaryGuild().GetCopilotAgentModel()
}

// GetCopilotAgentBrainChannelID returns the channel ID whose messages are loaded
// as extra moderator guidance for the agent. Empty means the feature is off.
func (c *Config) GetCopilotAgentBrainChannelID() string {
	return c.PrimaryGuild().GetCopilotAgentBrainChannelID()
}

// GetCopilotAgentBrainMaxItems returns the cap on how many brain-channel
// messages become guidance items (default 50).
func (c *Config) GetCopilotAgentBrainMaxItems() int {
	return c.PrimaryGuild().GetCopilotAgentBrainMaxItems()
}

// GetCopilotAgentBrainMaxChars returns the cap on total guidance characters
// injected into the prompt (default 4000).
func (c *Config) GetCopilotAgentBrainMaxChars() int {
	return c.PrimaryGuild().GetCopilotAgentBrainMaxChars()
}

// defaultBrainRefreshInterval is used when the brain refresh interval is unset
// or invalid.
const defaultBrainRefreshInterval = 5 * time.Minute

// GetCopilotAgentBrainRefreshInterval returns how often the agent reloads the
// brain channel. It defaults to 5m when unset, and warns then falls back to 5m
// when the configured value is unparseable or non-positive. Read once at
// startup; changing it requires a restart.
func (c *Config) GetCopilotAgentBrainRefreshInterval() time.Duration {
	raw := c.PrimaryGuild().resolveString(KeyCopilotAgentBrainRefreshInterval)
	d, reason := parseBrainRefreshInterval(raw, defaultBrainRefreshInterval)
	if reason != "" {
		c.Logger.Warnf("agent: brain refresh interval %q %s; using %s", strings.TrimSpace(raw), reason, d)
	}
	return d
}

// parseBrainRefreshInterval parses a configured brain refresh interval. It
// returns def with a reason when raw is empty, unparseable, or non-positive,
// and the parsed duration with an empty reason otherwise.
func parseBrainRefreshInterval(raw string, def time.Duration) (time.Duration, string) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return def, ""
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return def, "is not a valid duration"
	}
	if d <= 0 {
		return def, "must be positive"
	}
	return d, ""
}

func (c *Config) GetGamerPalsModActionLogChannelID() string {
	return c.PrimaryGuild().GetGamerPalsModActionLogChannelID()
}

func (c *Config) GetGamerpalsLogChannelID() string {
	return c.PrimaryGuild().GetGamerpalsLogChannelID()
}

func (c *Config) GetGamerPalsIntroductionsForumChannelID() string {
	return c.PrimaryGuild().GetGamerPalsIntroductionsForumChannelID()
}

func (c *Config) GetGamerPalsHelpDeskChannelID() string {
	return c.PrimaryGuild().GetGamerPalsHelpDeskChannelID()
}

func (c *Config) GetGamerPalsLFGForumChannelID() string {
	return c.PrimaryGuild().GetGamerPalsLFGForumChannelID()
}

func (c *Config) GetGamerPalsVoiceSyncCategoryID() string {
	return c.PrimaryGuild().GetGamerPalsVoiceSyncCategoryID()
}

// GetGamerPals1984LogChannelID returns the channel ID where the 1984 module
// posts message activity logs (creates, edits, deletes, reactions).
func (c *Config) GetGamerPals1984LogChannelID() string {
	return c.PrimaryGuild().GetGamerPals1984LogChannelID()
}

// LFG Looking NOW panel channel ID (persisted so panel survives restarts)
func (c *Config) GetLFGNowPanelChannelID() string {
	return c.PrimaryGuild().GetLFGNowPanelChannelID()
}

// LFG Looking NOW role
func (c *Config) GetLFGNowRoleID() string {
	return c.PrimaryGuild().GetLFGNowRoleID()
}

func (c *Config) GetLFGNowRoleDuration() time.Duration {
	return c.PrimaryGuild().GetLFGNowRoleDuration()
}

// New Pals systems
// -----
func (c *Config) GetNewPalsSystemEnabled() bool {
	return c.PrimaryGuild().GetNewPalsSystemEnabled()
}

func (c *Config) GetNewPalsRoleID() string {
	return c.PrimaryGuild().GetNewPalsRoleID()
}

func (c *Config) GetNewPalsChannelID() string {
	return c.PrimaryGuild().GetNewPalsChannelID()
}

func (c *Config) GetNewPalsKeepRoleDuration() time.Duration {
	return c.PrimaryGuild().GetNewPalsKeepRoleDuration()
}

func (c *Config) GetNewPalsTimeBetweenWelcomeMessages() time.Duration {
	return c.PrimaryGuild().GetNewPalsTimeBetweenWelcomeMessages()
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

// Set updates a config value in memory for the current process only. It is used
// for runtime-refreshed secrets (e.g. the IGDB token) that are not persisted to
// the database. Per-guild operational settings are persisted via GuildConfig
// overrides, not this method.
func (c *Config) Set(key string, value any) {
	c.v.Set(key, value)
}

// GetString returns the string value for a given config key
func (c *Config) GetString(key string) string {
	return c.v.GetString(key)
}

// Event Feed configuration
// -----

// GetEventFeedChannelID returns the channel ID where new scheduled events are forwarded
func (c *Config) GetEventFeedChannelID() string {
	return c.PrimaryGuild().GetEventFeedChannelID()
}

// Introduction Feed configuration
// -----

// GetIntroFeedChannelID returns the channel ID where intro posts are forwarded
func (c *Config) GetIntroFeedChannelID() string {
	return c.PrimaryGuild().GetIntroFeedChannelID()
}

// GetIntroFeedRateLimitHours returns the number of hours between allowed feed posts per user (default 48)
func (c *Config) GetIntroFeedRateLimitHours() int {
	return c.PrimaryGuild().GetIntroFeedRateLimitHours()
}

// GetIntroFeedBoosterRateLimitHours returns the number of hours between allowed feed posts for
// server (Nitro) boosters. A value <= 0 means it is not configured, in which case boosters use
// the standard rate limit (GetIntroFeedRateLimitHours).
func (c *Config) GetIntroFeedBoosterRateLimitHours() int {
	return c.PrimaryGuild().GetIntroFeedBoosterRateLimitHours()
}

// Translate language configuration
// -----

// GetTranslateLanguage returns the configured translate language style.
// Valid values: "caveman", "gen_alpha", "old_man", "80s", "high_society", "random"
func (c *Config) GetTranslateLanguage() string {
	return c.PrimaryGuild().GetTranslateLanguage()
}

// ScamGuard (anti-scam image detection)
// -----

// GetScamGuardEnabled returns the master switch for the scamguard module. When
// false (default), no image hashing or enforcement happens.
func (c *Config) GetScamGuardEnabled() bool {
	return c.PrimaryGuild().GetScamGuardEnabled()
}

// GetScamGuardHashThreshold returns the maximum Hamming distance between a
// 64-bit perceptual hash and a known-bad hash for the two to be considered a
// match. 0 means an exact match; higher is looser. Defaults to 8 and is capped
// at 16: with a 64-bit hash, larger thresholds match nearly anything and would
// mass-action innocent users, so values above 16 are treated as 16.
func (c *Config) GetScamGuardHashThreshold() int {
	return c.PrimaryGuild().GetScamGuardHashThreshold()
}

// GetScamGuardAction returns the action taken on a hash match: "log" (log
// only), "delete" (delete the message and log), or "timeout" (delete, time the
// author out, and log). Defaults to "timeout".
func (c *Config) GetScamGuardAction() string {
	return c.PrimaryGuild().GetScamGuardAction()
}

// GetScamGuardTimeoutDuration returns how long a matched author is timed out
// when the action is "timeout". Defaults to 168h (7 days). Discord caps
// timeouts at 28 days; callers should clamp if needed.
func (c *Config) GetScamGuardTimeoutDuration() time.Duration {
	return c.PrimaryGuild().GetScamGuardTimeoutDuration()
}

// GetScamGuardLogChannelID returns the channel where scamguard actions are
// logged. Falls back to the mod action log channel when unset.
func (c *Config) GetScamGuardLogChannelID() string {
	return c.PrimaryGuild().GetScamGuardLogChannelID()
}

// GetScamGuardSeedHashesPath returns an optional path to an external seed file
// of known-bad hashes loaded at startup in addition to the embedded seed.
func (c *Config) GetScamGuardSeedHashesPath() string {
	return c.v.GetString("scamguard_seed_hashes_path")
}
