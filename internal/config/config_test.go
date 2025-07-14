package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	t.Run("missing required variables", func(t *testing.T) {
		_, err := NewConfig()
		require.Error(t, err)
		require.Contains(t, err.Error(), "bot_token is required")
	})

	t.Run("new prefixed environment variables", func(t *testing.T) {
		t.Setenv("GAMERPAL_BOT_TOKEN", "prefixed_token")
		t.Setenv("GAMERPAL_IGDB_CLIENT_ID", "prefixed_id")
		t.Setenv("GAMERPAL_IGDB_CLIENT_TOKEN", "prefixed_token")

		cfg, err := NewConfig()
		require.NoError(t, err)

		require.Equal(t, "prefixed_token", cfg.GetBotToken())
		require.Equal(t, "prefixed_id", cfg.GetIGDBClientID())
		require.Equal(t, "prefixed_token", cfg.GetIGDBClientToken())
	})

	t.Run("partial configuration - missing igdb client id", func(t *testing.T) {
		t.Setenv("GAMERPAL_BOT_TOKEN", "test_token")
		t.Setenv("GAMERPAL_IGDB_CLIENT_TOKEN", "test_token")

		_, err := NewConfig()
		require.Error(t, err)
		require.Contains(t, err.Error(), "igdb_client_id is required")
	})

	t.Run("partial configuration - missing igdb client token", func(t *testing.T) {
		t.Setenv("GAMERPAL_BOT_TOKEN", "test_token")
		t.Setenv("GAMERPAL_IGDB_CLIENT_ID", "test_id")

		_, err := NewConfig()
		require.Error(t, err)
		require.Contains(t, err.Error(), "igdb_client_token is required")
	})
}
