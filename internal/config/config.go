package config

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/log"

	"github.com/spf13/viper"
)

type Config struct {
	v      *viper.Viper
	Logger *log.Logger

	// store backs per-guild config overrides. When nil (e.g. mock configs in
	// tests, or before wiring), all per-guild reads fall back to env/default.
	store GuildStore
	// registry is the collected set of module-declared settings that drives
	// the config panel. Populated at startup via ApplyRegistry.
	registry *Registry

	// overrideCache memoizes each guild's full override set so per-guild reads
	// (some on the every-message hot path) avoid a SQLite round-trip per call.
	// A guild is loaded once via the store's AllGuildConfig and then mutated in
	// place by SetOverride/ClearOverride, which are the only writers. nil map
	// entry means "not loaded yet".
	overrideCacheMu sync.RWMutex
	overrideCache   map[string]map[string]string
}

// SetGuildStore wires the per-guild override store. Called once at startup
// after the database is opened, before any per-guild read. It also clears the
// override cache so a re-wire does not serve stale values.
func (c *Config) SetGuildStore(store GuildStore) {
	c.overrideCacheMu.Lock()
	c.store = store
	c.overrideCache = nil
	c.overrideCacheMu.Unlock()
}

// ApplyRegistry records the collected settings registry. The registry is the
// single source of truth the config panel renders from; getter-level defaults
// remain authoritative for reads.
func (c *Config) ApplyRegistry(r *Registry) {
	c.registry = r
}

// Registry returns the collected settings registry, or nil before startup
// wiring. The config panel reads this lazily at interaction time.
func (c *Config) Registry() *Registry {
	return c.registry
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

	// Bind environment variables.
	//
	// AutomaticEnv with the GAMERPAL_ prefix means any config key read via
	// v.Get*("foo_bar") also checks the GAMERPAL_FOO_BAR environment
	// variable. New config keys "just work" without needing to be added to
	// an explicit binding list.
	v.SetEnvPrefix("GAMERPAL")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Always log to stderr; optionally also tee to a rotating log file.
	writers := []io.Writer{os.Stderr}
	if !v.GetBool("disable_file_logging") {
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
		writers = append(writers, newLogFile)
	}
	w := io.MultiWriter(writers...)

	newCfg := &Config{
		v: v,
		Logger: log.NewWithOptions(w, log.Options{
			ReportCaller:    true,
			ReportTimestamp: true,
			TimeFormat:      time.Kitchen,
		}),
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

	// Generate a timestamped log file name in yyyy-mm-dd format
	fileName := fmt.Sprintf("gamerpal_%s.log", time.Now().Format("2006-01-02"))

	// If it exists, just return the existing file
	if _, err := os.Stat(filepath.Join(dir, fileName)); err == nil {
		return os.OpenFile(filepath.Join(dir, fileName), os.O_APPEND|os.O_WRONLY, 0644)
	}

	// Otherwise, create a new log file
	file, err := os.Create(filepath.Join(dir, fileName))
	if err != nil {
		return nil, err
	}
	return file, nil
}

func (c *Config) RotateAndPruneLogs() error {
	if c.v.GetBool("disable_file_logging") {
		// File logging is disabled (e.g. when running in a container that
		// forwards stdout to a log aggregator), so there's nothing to rotate.
		return nil
	}

	// First rotate the log file
	newLogFile, err := newLogFile(c.v.GetString("log_dir"))
	if err != nil {
		return fmt.Errorf("failed to rotate and create new log file: %w", err)
	}

	w := io.MultiWriter(os.Stderr, newLogFile)
	c.Logger.SetOutput(w)

	// After rotating, we can prune old log files
	err = pruneOldLogFiles(c.v.GetString("log_dir"))
	if err != nil {
		return fmt.Errorf("failed to prune old log files: %w", err)
	}

	c.Logger.Info("Log file rotated and old logs pruned successfully")

	return nil
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

		// Check if the file is older than 3 days
		info, err := file.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > 3*24*time.Hour {
			if err := os.Remove(filepath.Join(dir, file.Name())); err != nil {
				return fmt.Errorf("failed to remove old log file %s: %w", file.Name(), err)
			}
		}
	}

	return nil
}

// NewMockConfig creates a mock configuration for testing
func NewMockConfig(kv map[string]any) *Config {
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
	v.SetDefault("translate_language", "random")
	v.SetDefault("disable_file_logging", false)

	// scamguard (anti-scam image detection) defaults
	v.SetDefault("scamguard_enabled", false)
	v.SetDefault("scamguard_hash_threshold", 8)
	v.SetDefault("scamguard_action", "timeout")
	v.SetDefault("scamguard_timeout_duration", "168h")
}

// validateConfig validates that all required configuration fields are present
func validateConfig(cfg *Config) error {
	if cfg.v.GetString("bot_token") == "" {
		return fmt.Errorf("bot_token is required (set GAMERPAL_BOT_TOKEN environment variable)")
	}

	if cfg.v.GetString("igdb_client_id") == "" {
		cfg.Logger.Warn("igdb_client_id is not set (set GAMERPAL_IGDB_CLIENT_ID environment variable)")
	}

	if cfg.v.GetString("igdb_client_secret") == "" {
		cfg.Logger.Warn("igdb_client_secret is not set (set GAMERPAL_IGDB_CLIENT_SECRET environment variable)")
	}

	if cfg.v.GetString("igdb_client_token") == "" {
		cfg.Logger.Warn("igdb_client_token is not set (set GAMERPAL_IGDB_CLIENT_TOKEN environment variable)")
	}

	return nil
}
