package config

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GAMERPAL_LOG_DIR", tmpDir)
	t.Setenv("GAMERPAL_DATABASE_PATH", tmpDir+"/test.db")

	t.Run("missing required variables", func(t *testing.T) {
		_, err := NewConfig()
		require.Error(t, err)
	})

	t.Run("new prefixed environment variables", func(t *testing.T) {
		t.Setenv("GAMERPAL_BOT_TOKEN", "prefixed_token")
		t.Setenv("GAMERPAL_IGDB_CLIENT_ID", "prefixed_id")
		t.Setenv("GAMERPAL_IGDB_CLIENT_SECRET", "prefixed_secret")
		t.Setenv("GAMERPAL_IGDB_CLIENT_TOKEN", "prefixed_token")

		cfg, err := NewConfig()
		require.NoError(t, err)

		require.Equal(t, "prefixed_token", cfg.GetBotToken())
		require.Equal(t, "prefixed_id", cfg.GetIGDBClientID())
		require.Equal(t, "prefixed_secret", cfg.GetIGDBClientSecret())
		require.Equal(t, "prefixed_token", cfg.GetIGDBClientToken())
	})

	t.Run("partial configuration - missing igdb data doesn't cause startup fail", func(t *testing.T) {
		t.Setenv("GAMERPAL_BOT_TOKEN", "test_token")

		_, err := NewConfig()
		require.NoError(t, err)
	})

	t.Run("disable file logging skips log file creation", func(t *testing.T) {
		cleanDir := t.TempDir()
		t.Setenv("GAMERPAL_BOT_TOKEN", "test_token")
		t.Setenv("GAMERPAL_LOG_DIR", cleanDir)
		t.Setenv("GAMERPAL_DISABLE_FILE_LOGGING", "true")

		cfg, err := NewConfig()
		require.NoError(t, err)
		require.True(t, cfg.GetDisableFileLogging())

		require.NoError(t, cfg.RotateAndPruneLogs())

		entries, err := os.ReadDir(cleanDir)
		require.NoError(t, err)
		for _, e := range entries {
			require.False(t, strings.HasPrefix(e.Name(), "gamerpal_"),
				"unexpected log file %q created when file logging was disabled", e.Name())
		}
	})

	t.Run("AutomaticEnv exposes arbitrary keys via GAMERPAL_ prefix", func(t *testing.T) {
		t.Setenv("GAMERPAL_BOT_TOKEN", "test_token")
		t.Setenv("GAMERPAL_GAMERPALS_SERVER_ID", "server-from-env")
		t.Setenv("GAMERPAL_GAMERPALS_MOD_ACTION_LOG_CHANNEL_ID", "mod-log-from-env")
		t.Setenv("GAMERPAL_TRANSLATE_LANGUAGE", "caveman")
		t.Setenv("GAMERPAL_NEW_PALS_SYSTEM_ENABLED", "true")
		t.Setenv("GAMERPAL_LFG_NOW_ROLE_DURATION", "48h")
		t.Setenv("GAMERPAL_SUPER_ADMINS", "admin1,admin2,admin3")

		cfg, err := NewConfig()
		require.NoError(t, err)

		require.Equal(t, "server-from-env", cfg.GetGamerPalsServerID())
		require.Equal(t, "mod-log-from-env", cfg.GetGamerPalsModActionLogChannelID())
		require.Equal(t, "caveman", cfg.GetTranslateLanguage())
		require.True(t, cfg.GetNewPalsSystemEnabled())
		require.Equal(t, 48*time.Hour, cfg.GetLFGNowRoleDuration())
		require.Equal(t, []string{"admin1", "admin2", "admin3"}, cfg.GetSuperAdmins())
	})
}

func TestGetSuperAdmins(t *testing.T) {
	// Note: the env-var CSV split path is already exercised end-to-end by
	// TestLoad/AutomaticEnv_exposes_arbitrary_keys_via_GAMERPAL__prefix.
	// These tests cover behaviors that path doesn't reach: YAML-style
	// slice values (single element with a literal comma, whitespace
	// trimming, empty slice).

	t.Run("YAML slice with single element containing a comma is not split", func(t *testing.T) {
		_, present := os.LookupEnv("GAMERPAL_SUPER_ADMINS")
		require.False(t, present, "test pollution: GAMERPAL_SUPER_ADMINS must be unset")

		cfg := NewMockConfig(map[string]interface{}{
			"super_admins": []string{"weird,id,with,commas"},
		})
		require.Equal(t, []string{"weird,id,with,commas"}, cfg.GetSuperAdmins())
	})

	t.Run("YAML slice entries are trimmed and empties dropped", func(t *testing.T) {
		_, present := os.LookupEnv("GAMERPAL_SUPER_ADMINS")
		require.False(t, present, "test pollution: GAMERPAL_SUPER_ADMINS must be unset")

		cfg := NewMockConfig(map[string]interface{}{
			"super_admins": []string{"  admin1  ", "", "admin2", "   "},
		})
		require.Equal(t, []string{"admin1", "admin2"}, cfg.GetSuperAdmins())
	})

	t.Run("empty slice returns nil", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{
			"super_admins": []string{},
		})
		require.Nil(t, cfg.GetSuperAdmins())
	})
}

func TestScamGuardDefaults(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("GAMERPAL_LOG_DIR", tmpDir)
	t.Setenv("GAMERPAL_DATABASE_PATH", tmpDir+"/test.db")
	t.Setenv("GAMERPAL_BOT_TOKEN", "test_token")

	cfg, err := NewConfig()
	require.NoError(t, err)

	require.False(t, cfg.GetScamGuardEnabled())
	require.Equal(t, 8, cfg.GetScamGuardHashThreshold())
	require.Equal(t, "timeout", cfg.GetScamGuardAction())
	require.Equal(t, 168*time.Hour, cfg.GetScamGuardTimeoutDuration())
}

func TestScamGuardAccessors(t *testing.T) {
	t.Run("explicit zero threshold is honored", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{"scamguard_hash_threshold": 0})
		require.Equal(t, 0, cfg.GetScamGuardHashThreshold())
	})

	t.Run("negative threshold falls back to default", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{"scamguard_hash_threshold": -3})
		require.Equal(t, 8, cfg.GetScamGuardHashThreshold())
	})

	t.Run("oversized threshold is capped at 16", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{"scamguard_hash_threshold": 64})
		require.Equal(t, 16, cfg.GetScamGuardHashThreshold())
	})

	t.Run("threshold within range is honored", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{"scamguard_hash_threshold": 12})
		require.Equal(t, 12, cfg.GetScamGuardHashThreshold())
	})

	t.Run("invalid action falls back to timeout", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{"scamguard_action": "explode"})
		require.Equal(t, "timeout", cfg.GetScamGuardAction())
	})

	t.Run("valid action is honored", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{"scamguard_action": "delete"})
		require.Equal(t, "delete", cfg.GetScamGuardAction())
	})

	t.Run("log channel falls back to mod action log channel", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{
			"gamerpals_mod_action_log_channel_id": "MODLOG",
		})
		require.Equal(t, "MODLOG", cfg.GetScamGuardLogChannelID())
	})

	t.Run("explicit log channel wins", func(t *testing.T) {
		cfg := NewMockConfig(map[string]interface{}{
			"scamguard_log_channel_id":            "SCAMLOG",
			"gamerpals_mod_action_log_channel_id": "MODLOG",
		})
		require.Equal(t, "SCAMLOG", cfg.GetScamGuardLogChannelID())
	})
}
