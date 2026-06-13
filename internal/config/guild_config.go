package config

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

var (
	errNoStore = errors.New("config: no guild store configured")
	errNoGuild = errors.New("config: no guild bound")
)

// GuildConfig is a guild-bound view over *Config. Per-guild settings resolve in
// this order: a stored override for the guild, then the env var / built-in
// default (the existing viper-backed value). Until an admin saves an override,
// behavior is identical to reading from Config directly, which makes the whole
// per-guild layer an additive no-op.
//
// GuildConfig embeds *Config so it also exposes the global getters and Logger.
// The per-guild getters defined on GuildConfig shadow the thin delegators of
// the same name on *Config.
type GuildConfig struct {
	*Config
	guildID string
}

// ForGuild returns a guild-bound view for the given guild ID. A nil store
// (e.g. mock configs in tests) or empty guild ID transparently falls back to
// env/default reads.
func (c *Config) ForGuild(guildID string) *GuildConfig {
	return &GuildConfig{Config: c, guildID: guildID}
}

// PrimaryGuild returns a view bound to the configured GamerPals server. Used by
// bootstrap, schedulers, and services that have no interaction/event guild in
// scope. In the bot's single-active-guild model this is the operating guild.
func (c *Config) PrimaryGuild() *GuildConfig {
	return c.ForGuild(c.GetGamerPalsServerID())
}

// GuildID returns the guild this view is bound to.
func (gc *GuildConfig) GuildID() string { return gc.guildID }

// OverrideValue returns the stored per-guild override for a key and whether one
// exists. Used by the config panel to show whether a value is customized.
func (gc *GuildConfig) OverrideValue(key string) (string, bool) {
	return gc.override(key)
}

// EnvString returns the env/file/default string value for a key, ignoring any
// stored override. Used by the panel to show the fallback a reset reverts to.
func (gc *GuildConfig) EnvString(key string) string {
	return gc.v.GetString(key)
}

// SetOverride persists a per-guild override, recording the editor's user ID,
// and updates the in-memory cache so the next read reflects it immediately.
func (gc *GuildConfig) SetOverride(key, value, updatedBy string) error {
	if gc.store == nil {
		return errNoStore
	}
	if gc.guildID == "" {
		return errNoGuild
	}
	if err := gc.store.SetGuildConfigValue(gc.guildID, key, value, updatedBy); err != nil {
		return err
	}
	gc.cacheStore(gc.guildID, key, value)
	return nil
}

// ClearOverride removes a per-guild override, reverting the key to env/default,
// and updates the in-memory cache.
func (gc *GuildConfig) ClearOverride(key string) error {
	if gc.store == nil {
		return errNoStore
	}
	if gc.guildID == "" {
		return errNoGuild
	}
	if err := gc.store.DeleteGuildConfigValue(gc.guildID, key); err != nil {
		return err
	}
	gc.cacheClear(gc.guildID, key)
	return nil
}

// override returns the stored override value for a key and whether it exists.
// Missing store, missing guild, or a read error all resolve to "no override"
// so reads degrade to env/default rather than failing.
func (gc *GuildConfig) override(key string) (string, bool) {
	return gc.lookupOverride(gc.guildID, key)
}

// lookupOverride resolves a single override from the per-guild cache, loading
// the guild's full override set from the store on first access. All map access
// happens under the lock; on a store error it degrades to "no override" without
// caching, so a later call retries.
func (c *Config) lookupOverride(guildID, key string) (string, bool) {
	if c.store == nil || guildID == "" {
		return "", false
	}

	c.overrideCacheMu.RLock()
	if m, ok := c.overrideCache[guildID]; ok {
		v, found := m[key]
		c.overrideCacheMu.RUnlock()
		return v, found
	}
	c.overrideCacheMu.RUnlock()

	c.overrideCacheMu.Lock()
	defer c.overrideCacheMu.Unlock()
	// Re-check: another goroutine may have loaded while we waited for the lock.
	m, ok := c.overrideCache[guildID]
	if !ok {
		loaded, err := c.store.AllGuildConfig(guildID)
		if err != nil {
			c.Logger.Warnf("guild config: load overrides for guild %s failed: %v", guildID, err)
			return "", false
		}
		if loaded == nil {
			loaded = map[string]string{}
		}
		if c.overrideCache == nil {
			c.overrideCache = map[string]map[string]string{}
		}
		c.overrideCache[guildID] = loaded
		m = loaded
	}
	v, found := m[key]
	return v, found
}

// cacheStore updates the cached override for a loaded guild. If the guild is not
// loaded, it is a no-op: the value is already persisted, and the next read will
// load the full set (including this write) from the store.
func (c *Config) cacheStore(guildID, key, value string) {
	c.overrideCacheMu.Lock()
	defer c.overrideCacheMu.Unlock()
	if m, ok := c.overrideCache[guildID]; ok {
		m[key] = value
	}
}

// cacheClear removes a cached override for a loaded guild (no-op if unloaded).
func (c *Config) cacheClear(guildID, key string) {
	c.overrideCacheMu.Lock()
	defer c.overrideCacheMu.Unlock()
	if m, ok := c.overrideCache[guildID]; ok {
		delete(m, key)
	}
}

func (gc *GuildConfig) resolveString(key string) string {
	if v, ok := gc.override(key); ok {
		return v
	}
	return gc.v.GetString(key)
}

func (gc *GuildConfig) resolveBool(key string) bool {
	if v, ok := gc.override(key); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	return gc.v.GetBool(key)
}

// resolveInt reports the resolved integer and whether the key is set anywhere
// (override or viper). The bool mirrors viper.IsSet semantics so getters can
// apply their own "unset" defaults.
func (gc *GuildConfig) resolveInt(key string) (int, bool) {
	if v, ok := gc.override(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n, true
		}
	}
	if gc.v.IsSet(key) {
		return gc.v.GetInt(key), true
	}
	return 0, false
}

func (gc *GuildConfig) resolveDuration(key string) time.Duration {
	if v, ok := gc.override(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return gc.v.GetDuration(key)
}

// Shared server channels / categories
// -----

func (gc *GuildConfig) GetGamerPalsModActionLogChannelID() string {
	return gc.resolveString(KeyModActionLogChannelID)
}

func (gc *GuildConfig) GetGamerpalsLogChannelID() string {
	return gc.resolveString(KeyLogChannelID)
}

func (gc *GuildConfig) GetGamerPalsVoiceSyncCategoryID() string {
	return gc.resolveString(KeyVoiceSyncCategoryID)
}

func (gc *GuildConfig) GetGamerPalsHelpDeskChannelID() string {
	return gc.resolveString(KeyHelpDeskChannelID)
}

func (gc *GuildConfig) GetEventFeedChannelID() string {
	return gc.resolveString(KeyEventFeedChannelID)
}

// Introductions
// -----

func (gc *GuildConfig) GetGamerPalsIntroductionsForumChannelID() string {
	return gc.resolveString(KeyIntroductionsForumChannelID)
}

func (gc *GuildConfig) GetIntroFeedChannelID() string {
	return gc.resolveString(KeyIntroFeedChannelID)
}

// GetIntroFeedRateLimitHours returns the hours between allowed feed posts per
// user. A value <= 0 (or unset) means the 48-hour default.
func (gc *GuildConfig) GetIntroFeedRateLimitHours() int {
	hours, ok := gc.resolveInt(KeyIntroFeedRateLimitHours)
	if !ok || hours <= 0 {
		return 48
	}
	return hours
}

// GetIntroFeedBoosterRateLimitHours returns the hours between allowed feed
// posts for boosters. A value <= 0 means boosters use the standard rate limit.
func (gc *GuildConfig) GetIntroFeedBoosterRateLimitHours() int {
	hours, _ := gc.resolveInt(KeyIntroFeedBoosterRateLimit)
	return hours
}

// LFG
// -----

func (gc *GuildConfig) GetGamerPalsLFGForumChannelID() string {
	return gc.resolveString(KeyLFGForumChannelID)
}

func (gc *GuildConfig) GetLFGNowPanelChannelID() string {
	return gc.resolveString(KeyLFGNowPanelChannelID)
}

func (gc *GuildConfig) GetLFGNowRoleID() string {
	return gc.resolveString(KeyLFGNowRoleID)
}

func (gc *GuildConfig) GetLFGNowRoleDuration() time.Duration {
	return gc.resolveDuration(KeyLFGNowRoleDuration)
}

// New Pals
// -----

func (gc *GuildConfig) GetNewPalsSystemEnabled() bool {
	return gc.resolveBool(KeyNewPalsSystemEnabled)
}

func (gc *GuildConfig) GetNewPalsRoleID() string {
	return gc.resolveString(KeyNewPalsRoleID)
}

func (gc *GuildConfig) GetNewPalsChannelID() string {
	return gc.resolveString(KeyNewPalsChannelID)
}

func (gc *GuildConfig) GetNewPalsKeepRoleDuration() time.Duration {
	return gc.resolveDuration(KeyNewPalsKeepRoleDuration)
}

func (gc *GuildConfig) GetNewPalsTimeBetweenWelcomeMessages() time.Duration {
	return gc.resolveDuration(KeyNewPalsTimeBetweenMsgs)
}

// 1984
// -----

func (gc *GuildConfig) GetGamerPals1984LogChannelID() string {
	return gc.resolveString(Key1984LogChannelID)
}

// Translate
// -----

// GetTranslateLanguage returns the configured translate style, defaulting to
// "random" when unset.
func (gc *GuildConfig) GetTranslateLanguage() string {
	if lang := gc.resolveString(KeyTranslateLanguage); lang != "" {
		return lang
	}
	return "random"
}

// ScamGuard
// -----

func (gc *GuildConfig) GetScamGuardEnabled() bool {
	return gc.resolveBool(KeyScamGuardEnabled)
}

// GetScamGuardHashThreshold returns the max Hamming distance for a match.
// Defaults to 8 when unset or negative, and is capped at 16.
func (gc *GuildConfig) GetScamGuardHashThreshold() int {
	t, ok := gc.resolveInt(KeyScamGuardHashThreshold)
	if !ok || t < 0 {
		return 8
	}
	if t > 16 {
		return 16
	}
	return t
}

// GetScamGuardAction returns "log", "delete", or "timeout" (default).
func (gc *GuildConfig) GetScamGuardAction() string {
	switch a := strings.ToLower(strings.TrimSpace(gc.resolveString(KeyScamGuardAction))); a {
	case "log", "delete", "timeout":
		return a
	default:
		return "timeout"
	}
}

// GetScamGuardTimeoutDuration returns the timeout applied on a match, defaulting
// to 168h (7 days) when unset or non-positive.
func (gc *GuildConfig) GetScamGuardTimeoutDuration() time.Duration {
	d := gc.resolveDuration(KeyScamGuardTimeoutDuration)
	if d <= 0 {
		return 168 * time.Hour
	}
	return d
}

// GetScamGuardLogChannelID returns the scamguard log channel, falling back to
// the mod action log channel when unset.
func (gc *GuildConfig) GetScamGuardLogChannelID() string {
	if id := gc.resolveString(KeyScamGuardLogChannelID); id != "" {
		return id
	}
	return gc.GetGamerPalsModActionLogChannelID()
}

// Agent gating
// -----

func (gc *GuildConfig) GetCopilotAgentRoleID() string {
	return gc.resolveString(KeyCopilotAgentRoleID)
}

func (gc *GuildConfig) GetCopilotAgentExcludeRoleID() string {
	return gc.resolveString(KeyCopilotAgentExcludeRoleID)
}

// GetCopilotAgentReplyChannelAllowlist returns the allowlisted channel IDs. A
// stored override is parsed as CSV; otherwise the env var/viper value is used
// (mirroring the single-element CSV split viper applies to env values).
func (gc *GuildConfig) GetCopilotAgentReplyChannelAllowlist() []string {
	if v, ok := gc.override(KeyCopilotAgentReplyAllowlist); ok {
		return splitTrimCSV(v)
	}
	raw := gc.v.GetStringSlice(KeyCopilotAgentReplyAllowlist)
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

// GetCopilotAgentModel returns the agent model, defaulting to "gpt-5.5".
func (gc *GuildConfig) GetCopilotAgentModel() string {
	if v := gc.resolveString(KeyCopilotAgentModel); v != "" {
		return v
	}
	return "gpt-5.5"
}

// splitTrimCSV splits a comma-separated string, trimming entries and dropping
// empties. Returns nil when nothing remains.
func splitTrimCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
