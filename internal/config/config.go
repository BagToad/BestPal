package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/charmbracelet/log"

	"github.com/spf13/viper"
)

type Config struct {
	v      *viper.Viper
	Logger *log.Logger
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
		// Config file can't be read, continue with env vars and defaults
		l := log.New(os.Stderr)
		l.Warnf("error reading config file: %v\nContinuing with envs...", err)
	}

	// Bind environment variables
	err := bindEnvs(v)
	if err != nil {
		// If env binding also fails, we'll basically have no config
		// and need to exit at this point.
		return nil, fmt.Errorf("error binding environment variables: %w", err)
	}

	newLogFile, err := newLogFile(v.GetString("log_dir"))
	if err != nil {
		// I've decided to make this fatal because I want
		// to know if that's an issue.
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}

	if err := pruneOldLogFiles(v.GetString("log_dir")); err != nil {
		// This too. I've decided to make it fatal.
		return nil, fmt.Errorf("failed to prune old log files: %w", err)
	}

	// Log both to a file and to stderr
	w := io.MultiWriter(os.Stderr, newLogFile)

	newCfg := &Config{
		v:      v,
		Logger: log.New(w),
	}

	// Validate required fields
	if err := validateConfig(newCfg); err != nil {
		return nil, err
	}

	return newCfg, nil
}

// newLogFile generates a new log file
func newLogFile(dir string) (*os.File, error) {
	if dir == "" {
		return nil, fmt.Errorf("log directory is not set")
	}

	// Create dir if it doesn't exist
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create a new log file with timestamp
	file, err := os.Create(fmt.Sprintf("%s/gamerpal_%s.log", dir, time.Now().Format("20060102_150405")))
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (c *Config) PruneOldLogFiles() error {
	return pruneOldLogFiles(c.v.GetString("log_dir"))
}

// pruneOldLogFiles removes log files older than 7 days
func pruneOldLogFiles(dir string) error {
	logFiles, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("failed to read log directory: %w", err)
	}

	for _, file := range logFiles {
		if file.IsDir() {
			continue
		}

		// Check if the file is older than 7 days
		info, err := file.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > 7*24*time.Hour {
			if err := os.Remove(filepath.Join("logs", file.Name())); err != nil {
				return fmt.Errorf("failed to remove old log file %s: %w", file.Name(), err)
			}
		}
	}

	return nil
}

// NewMockConfig creates a mock configuration for testing
func NewMockConfig(kv map[string]interface{}) *Config {
	v := viper.New()
	for k, val := range kv {
		v.Set(k, val)
	}
	return &Config{
		v:      v,
		Logger: log.New(os.Stderr),
	}
}

// setDefaults sets default configuration values
func setDefaults(v *viper.Viper) {
	// Add any default values here if needed in the future
	v.SetDefault("log_dir", "./logs")
	v.SetDefault("database_path", "./gamerpal.db")
}

// bindEnvs binds environment variables to viper keys
func bindEnvs(v *viper.Viper) error {
	bindings := []struct {
		key string
		env string
	}{
		{"bot_token", "GAMERPAL_BOT_TOKEN"},
		{"igdb_client_id", "GAMERPAL_IGDB_CLIENT_ID"},
		{"igdb_client_token", "GAMERPAL_IGDB_CLIENT_TOKEN"},
		{"log_dir", "GAMERPAL_LOG_DIR"},
	}

	for _, binding := range bindings {
		if err := v.BindEnv(binding.key, binding.env); err != nil {
			return fmt.Errorf("error binding %s environment variable: %w", binding.key, err)
		}
	}
	return nil
}

// validateConfig validates that all required configuration fields are present
func validateConfig(cfg *Config) error {
	if cfg.v.GetString("bot_token") == "" {
		return fmt.Errorf("bot_token is required (set DISCORD_BOT_TOKEN or GAMERPAL_BOT_TOKEN environment variable)")
	}

	if cfg.v.GetString("igdb_client_id") == "" {
		cfg.Logger.Warn("igdb_client_id is not set (set IGDB_CLIENT_ID or GAMERPAL_IGDB_CLIENT_ID environment variable)")
	}

	if cfg.v.GetString("igdb_client_token") == "" {
		cfg.Logger.Warn("igdb_client_token is not set (set IGDB_CLIENT_TOKEN or GAMERPAL_IGDB_CLIENT_TOKEN environment variable)")
	}

	return nil
}
