package config

import (
	"testing"

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
}
