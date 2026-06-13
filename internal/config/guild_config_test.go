package config

import (
	"maps"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeStore is an in-memory GuildStore for testing per-guild resolution.
type fakeStore struct {
	values map[string]map[string]string // guildID -> key -> value
	err    error
}

func newFakeStore() *fakeStore {
	return &fakeStore{values: map[string]map[string]string{}}
}

func (f *fakeStore) AllGuildConfig(guildID string) (map[string]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]string{}
	maps.Copy(out, f.values[guildID])
	return out, nil
}

func (f *fakeStore) SetGuildConfigValue(guildID, key, value, updatedBy string) error {
	if f.err != nil {
		return f.err
	}
	if f.values[guildID] == nil {
		f.values[guildID] = map[string]string{}
	}
	f.values[guildID][key] = value
	return nil
}

func (f *fakeStore) DeleteGuildConfigValue(guildID, key string) error {
	if f.err != nil {
		return f.err
	}
	delete(f.values[guildID], key)
	return nil
}

func TestGuildConfigResolution(t *testing.T) {
	const guild = "G1"

	t.Run("override wins over env/default", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{
			"gamerpals_server_id":      guild,
			"scamguard_enabled":        false,
			"new_pals_system_enabled":  false,
			"lfg_now_role_duration":    "1h",
			"scamguard_hash_threshold": 8,
		})
		store := newFakeStore()
		cfg.SetGuildStore(store)

		// Before any override, reads match env/default.
		require.False(t, cfg.GetScamGuardEnabled())
		require.Equal(t, time.Hour, cfg.GetLFGNowRoleDuration())

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyScamGuardEnabled, "true", "U1"))
		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyLFGNowRoleDuration, "48h", "U1"))
		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyScamGuardHashThreshold, "12", "U1"))

		require.True(t, cfg.GetScamGuardEnabled())
		require.Equal(t, 48*time.Hour, cfg.GetLFGNowRoleDuration())
		require.Equal(t, 12, cfg.GetScamGuardHashThreshold())
	})

	t.Run("delete reverts to env/default", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{
			"gamerpals_server_id": guild,
			"translate_language":  "caveman",
		})
		store := newFakeStore()
		cfg.SetGuildStore(store)

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyTranslateLanguage, "old_man", "U1"))
		require.Equal(t, "old_man", cfg.GetTranslateLanguage())

		require.NoError(t, cfg.ForGuild(guild).ClearOverride(KeyTranslateLanguage))
		require.Equal(t, "caveman", cfg.GetTranslateLanguage())
	})

	t.Run("override clamping matches getter semantics", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{"gamerpals_server_id": guild})
		store := newFakeStore()
		cfg.SetGuildStore(store)

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyScamGuardHashThreshold, "64", "U1"))
		require.Equal(t, 16, cfg.GetScamGuardHashThreshold(), "oversized override capped at 16")

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyScamGuardHashThreshold, "-3", "U1"))
		require.Equal(t, 8, cfg.GetScamGuardHashThreshold(), "negative override falls back to default")

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyScamGuardAction, "explode", "U1"))
		require.Equal(t, "timeout", cfg.GetScamGuardAction(), "invalid action falls back to timeout")
	})

	t.Run("unparseable override falls back to env/default", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{
			"gamerpals_server_id":   guild,
			"lfg_now_role_duration": "2h",
			"scamguard_enabled":     true,
		})
		store := newFakeStore()
		cfg.SetGuildStore(store)

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyLFGNowRoleDuration, "not-a-duration", "U1"))
		require.Equal(t, 2*time.Hour, cfg.GetLFGNowRoleDuration())

		require.NoError(t, cfg.ForGuild(guild).SetOverride(KeyScamGuardEnabled, "not-a-bool", "U1"))
		require.True(t, cfg.GetScamGuardEnabled())
	})

	t.Run("ForGuild isolates overrides per guild", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{
			"gamerpals_server_id": guild,
			"scamguard_action":    "timeout",
		})
		store := newFakeStore()
		cfg.SetGuildStore(store)

		require.NoError(t, cfg.ForGuild("G2").SetOverride(KeyScamGuardAction, "delete", "U1"))

		// Primary guild (G1) is unaffected by G2's override.
		require.Equal(t, "timeout", cfg.GetScamGuardAction())
		require.Equal(t, "delete", cfg.ForGuild("G2").GetScamGuardAction())
	})

	t.Run("nil store falls back to env/default", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{
			"gamerpals_server_id": guild,
			"scamguard_enabled":   true,
		})
		// No SetGuildStore call: store is nil.
		require.True(t, cfg.GetScamGuardEnabled())
		require.True(t, cfg.ForGuild(guild).GetScamGuardEnabled())
	})

	t.Run("store read error degrades to env/default", func(t *testing.T) {
		cfg := NewMockConfig(map[string]any{
			"gamerpals_server_id": guild,
			"scamguard_enabled":   true,
		})
		store := newFakeStore()
		store.err = errFakeStore
		cfg.SetGuildStore(store)

		require.True(t, cfg.GetScamGuardEnabled())
	})
}

var errFakeStore = &storeError{"boom"}

type storeError struct{ msg string }

func (e *storeError) Error() string { return e.msg }
