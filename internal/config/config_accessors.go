package config

import "time"

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

func (c *Config) GetGamerPalsPairingCategoryID() string {
	return c.v.GetString("gamerpals_pairing_category_id")
}

func (c *Config) GetGamerPalsIntroductionsForumChannelID() string {
	return c.v.GetString("gamerpals_introductions_forum_channel_id")
}

// New Pals systems
// -----
func (c *Config) GetNewPalsSystemEnabled() bool {
	return c.v.GetBool("new_pals_system_enabled")
}

func (c *Config) GetNewPalsRoleID() string {
	return c.v.GetString("new_pals_role_id")
}

func (c *Config) GetNewPalsChannelID() string {
	return c.v.GetString("new_pals_channel_id")
}
func (c *Config) GetNewPalsKeepRoleDuration() time.Duration {
	return c.v.GetDuration("new_pals_keep_role_duration")
}

func (c *Config) GetNewPalsTimeBetweenWelcomeMessages() time.Duration {
	return c.v.GetDuration("new_pals_time_between_welcome_messages")
}

func (c *Config) GetSuperAdmins() []string {
	superAdmins := c.v.GetStringSlice("super_admins")
	if len(superAdmins) == 0 {
		return nil
	}
	return superAdmins
}

func (c *Config) GetDatabasePath() string {
	dbPath := c.v.GetString("database_path")
	return dbPath
}

func (c *Config) GetLogDir() string {
	return c.v.GetString("log_dir")
}

func (c *Config) Set(key string, value interface{}) {
	c.v.Set(key, value)
	c.v.WriteConfig()
}
