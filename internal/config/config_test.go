package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	// Test with missing vars
	t.Setenv("DISCORD_BOT_TOKEN", "")
	t.Setenv("IGDB_CLIENT_ID", "")
	t.Setenv("IGDB_CLIENT_TOKEN", "")
	_, err := Load()
	require.Error(t, err)

	// Test with valid vars
	t.Setenv("DISCORD_BOT_TOKEN", "test_token")
	t.Setenv("IGDB_CLIENT_ID", "test_id")
	t.Setenv("IGDB_CLIENT_TOKEN", "test_token")
	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, "test_token", cfg.BotToken)
	require.Equal(t, "test_id", cfg.IGDBClientID)
	require.Equal(t, "test_token", cfg.IGDBClientToken)
}
