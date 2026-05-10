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
