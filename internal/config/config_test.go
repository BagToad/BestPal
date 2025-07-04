package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoad(t *testing.T) {
	// Test with missing token
	t.Setenv("DISCORD_BOT_TOKEN", "")
	_, err := Load()
	require.Error(t, err)

	// Test with valid token
	t.Setenv("DISCORD_BOT_TOKEN", "test_token")
	cfg, err := Load()
	require.NoError(t, err)

	require.Equal(t, "test_token", cfg.BotToken)
}
