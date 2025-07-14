package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	v *viper.Viper
}

func (c *Config) GetBotToken() string {
	return c.v.GetString("bot_token")
}

func (c *Config) GetIGDBClientID() string {
	return c.v.GetString("igdb_client_id")
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

func (c *Config) GetGamerPalsServerID() string {
	return c.v.GetString("gamerpals_server_id")
}

func (c *Config) GetGamerPalsModActionLogChannelID() string {
	return c.v.GetString("gamerpals_mod_action_log_channel_id")
}

func (c *Config) GetSuperAdmins() []string {
	superAdmins := c.v.GetStringSlice("super_admins")
	if len(superAdmins) == 0 {
		return nil
	}
	return superAdmins
}

func (c *Config) Set(key string, value interface{}) {
	c.v.Set(key, value)
	c.v.WriteConfig()
}

// NewConfig loads the configuration from various sources using viper
func NewConfig() (*Config, error) {
	v := viper.New()

	// Set config name and paths
	v.SetConfigName("config")
	v.SetConfigType("yaml")
	v.AddConfigPath(".")

	// Set defaults
	setDefaults(v)

	// Try to read config file (don't error if it doesn't exist)
	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
		// Config file not found, continue with env vars and defaults
	}

	v.BindEnv("bot_token", "GAMERPAL_BOT_TOKEN")
	v.BindEnv("igdb_client_id", "GAMERPAL_IGDB_CLIENT_ID")
	v.BindEnv("igdb_client_token", "GAMERPAL_IGDB_CLIENT_TOKEN")

	// Validate required fields
	if err := validateConfig(v); err != nil {
		return nil, err
	}

	return &Config{v: v}, nil
}

// NewMockConfig creates a mock configuration for testing
func NewMockConfig(kv map[string]interface{}) *Config {
	v := viper.New()
	for k, val := range kv {
		v.Set(k, val)
	}
	return &Config{v: v}
}

// setDefaults sets default configuration values
func setDefaults(v *viper.Viper) {
	// Add any default values here if needed in the future
}

// validateConfig validates that all required configuration fields are present
func validateConfig(v *viper.Viper) error {
	if v.GetString("bot_token") == "" {
		return fmt.Errorf("bot_token is required (set DISCORD_BOT_TOKEN or GAMERPAL_BOT_TOKEN environment variable)")
	}

	if v.GetString("igdb_client_id") == "" {
		return fmt.Errorf("igdb_client_id is required (set IGDB_CLIENT_ID or GAMERPAL_IGDB_CLIENT_ID environment variable)")
	}

	if v.GetString("igdb_client_token") == "" {
		return fmt.Errorf("igdb_client_token is required (set IGDB_CLIENT_TOKEN or GAMERPAL_IGDB_CLIENT_TOKEN environment variable)")
	}

	return nil
}
