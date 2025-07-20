package database

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQL database connection
type DB struct {
	conn *sql.DB
}

// RouletteSignup represents a user signed up for roulette
type RouletteSignup struct {
	ID        int       `json:"id"`
	UserID    string    `json:"user_id"`
	GuildID   string    `json:"guild_id"`
	CreatedAt time.Time `json:"created_at"`
}

// RouletteGame represents a game in a user's roulette list
type RouletteGame struct {
	ID       int    `json:"id"`
	UserID   string `json:"user_id"`
	GuildID  string `json:"guild_id"`
	GameName string `json:"game_name"`
	IGDBID   int    `json:"igdb_id"`
}

// RouletteSchedule represents a scheduled pairing
type RouletteSchedule struct {
	ID          int       `json:"id"`
	GuildID     string    `json:"guild_id"`
	ScheduledAt time.Time `json:"scheduled_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// NewDB creates a new database connection and initializes tables
func NewDB(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db := &DB{conn: conn}

	// Initialize tables
	if err := db.initTables(); err != nil {
		return nil, fmt.Errorf("failed to initialize tables: %w", err)
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// initTables creates the necessary database tables
func (db *DB) initTables() error {
	query := `
	CREATE TABLE IF NOT EXISTS roulette_signups (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		guild_id TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, guild_id)
	);

	CREATE INDEX IF NOT EXISTS idx_roulette_signups_guild_id ON roulette_signups(guild_id);
	CREATE INDEX IF NOT EXISTS idx_roulette_signups_user_id ON roulette_signups(user_id);

	CREATE TABLE IF NOT EXISTS roulette_games (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		guild_id TEXT NOT NULL,
		game_name TEXT NOT NULL,
		igdb_id INTEGER,
		UNIQUE(user_id, guild_id, game_name)
	);

	CREATE INDEX IF NOT EXISTS idx_roulette_games_user_id ON roulette_games(user_id);
	CREATE INDEX IF NOT EXISTS idx_roulette_games_guild_id ON roulette_games(guild_id);
	CREATE INDEX IF NOT EXISTS idx_roulette_games_igdb_id ON roulette_games(igdb_id);

	CREATE TABLE IF NOT EXISTS roulette_schedules (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		guild_id TEXT NOT NULL,
		scheduled_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_roulette_schedules_guild_id ON roulette_schedules(guild_id);
	CREATE INDEX IF NOT EXISTS idx_roulette_schedules_scheduled_at ON roulette_schedules(scheduled_at);
	`

	_, err := db.conn.Exec(query)
	return err
}

// Roulette signup methods

// AddRouletteSignup adds a user to the roulette signup list
func (db *DB) AddRouletteSignup(userID, guildID string) error {
	query := `
	INSERT INTO roulette_signups (user_id, guild_id)
	VALUES (?, ?)
	ON CONFLICT(user_id, guild_id) DO NOTHING
	`
	_, err := db.conn.Exec(query, userID, guildID)
	if err != nil {
		return fmt.Errorf("failed to add roulette signup: %w", err)
	}
	return nil
}

// RemoveRouletteSignup removes a user from the roulette signup list
func (db *DB) RemoveRouletteSignup(userID, guildID string) error {
	query := `DELETE FROM roulette_signups WHERE user_id = ? AND guild_id = ?`
	_, err := db.conn.Exec(query, userID, guildID)
	if err != nil {
		return fmt.Errorf("failed to remove roulette signup: %w", err)
	}
	return nil
}

// GetRouletteSignups returns all users signed up for roulette in a guild
func (db *DB) GetRouletteSignups(guildID string) ([]RouletteSignup, error) {
	query := `
	SELECT id, user_id, guild_id, created_at
	FROM roulette_signups
	WHERE guild_id = ?
	ORDER BY created_at
	`
	rows, err := db.conn.Query(query, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get roulette signups: %w", err)
	}
	defer rows.Close()

	var signups []RouletteSignup
	for rows.Next() {
		var signup RouletteSignup
		err := rows.Scan(&signup.ID, &signup.UserID, &signup.GuildID, &signup.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan roulette signup: %w", err)
		}
		signups = append(signups, signup)
	}
	return signups, nil
}

// IsUserSignedUp checks if a user is signed up for roulette
func (db *DB) IsUserSignedUp(userID, guildID string) (bool, error) {
	query := `SELECT COUNT(*) FROM roulette_signups WHERE user_id = ? AND guild_id = ?`
	var count int
	err := db.conn.QueryRow(query, userID, guildID).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check roulette signup: %w", err)
	}
	return count > 0, nil
}

// Roulette games methods

// AddRouletteGame adds a game to a user's roulette game list
func (db *DB) AddRouletteGame(userID, guildID, gameName string, igdbID int) error {
	query := `
	INSERT INTO roulette_games (user_id, guild_id, game_name, igdb_id)
	VALUES (?, ?, ?, ?)
	ON CONFLICT(user_id, guild_id, game_name) DO UPDATE SET igdb_id = excluded.igdb_id
	`
	_, err := db.conn.Exec(query, userID, guildID, gameName, igdbID)
	if err != nil {
		return fmt.Errorf("failed to add roulette game: %w", err)
	}
	return nil
}

// GetRouletteGames returns all games for a user
func (db *DB) GetRouletteGames(userID, guildID string) ([]RouletteGame, error) {
	query := `
	SELECT id, user_id, guild_id, game_name, igdb_id
	FROM roulette_games
	WHERE user_id = ? AND guild_id = ?
	ORDER BY game_name
	`
	rows, err := db.conn.Query(query, userID, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get roulette games: %w", err)
	}
	defer rows.Close()

	var games []RouletteGame
	for rows.Next() {
		var game RouletteGame
		err := rows.Scan(&game.ID, &game.UserID, &game.GuildID, &game.GameName, &game.IGDBID)
		if err != nil {
			return nil, fmt.Errorf("failed to scan roulette game: %w", err)
		}
		games = append(games, game)
	}
	return games, nil
}

// RemoveAllRouletteGames removes all games for a user
func (db *DB) RemoveAllRouletteGames(userID, guildID string) error {
	query := `DELETE FROM roulette_games WHERE user_id = ? AND guild_id = ?`
	_, err := db.conn.Exec(query, userID, guildID)
	if err != nil {
		return fmt.Errorf("failed to remove roulette games: %w", err)
	}
	return nil
}

// RemoveRouletteGame removes a specific game by name for a user
func (db *DB) RemoveRouletteGame(userID, guildID, gameName string) error {
	query := `DELETE FROM roulette_games WHERE user_id = ? AND guild_id = ? AND game_name = ?`
	result, err := db.conn.Exec(query, userID, guildID, gameName)
	if err != nil {
		return fmt.Errorf("failed to remove roulette game: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("game '%s' not found in your list", gameName)
	}

	return nil
}

// Roulette schedule methods

// SetRouletteSchedule sets the next scheduled pairing time
func (db *DB) SetRouletteSchedule(guildID string, scheduledAt time.Time) error {
	// First, clear any existing schedule for this guild
	_, err := db.conn.Exec(`DELETE FROM roulette_schedules WHERE guild_id = ?`, guildID)
	if err != nil {
		return fmt.Errorf("failed to clear existing schedule: %w", err)
	}

	// Insert the new schedule
	query := `INSERT INTO roulette_schedules (guild_id, scheduled_at) VALUES (?, ?)`
	_, err = db.conn.Exec(query, guildID, scheduledAt)
	if err != nil {
		return fmt.Errorf("failed to set roulette schedule: %w", err)
	}
	return nil
}

// GetRouletteSchedule gets the next scheduled pairing time for a guild
func (db *DB) GetRouletteSchedule(guildID string) (*RouletteSchedule, error) {
	query := `
	SELECT id, guild_id, scheduled_at, created_at
	FROM roulette_schedules
	WHERE guild_id = ?
	ORDER BY scheduled_at ASC
	LIMIT 1
	`
	row := db.conn.QueryRow(query, guildID)

	var schedule RouletteSchedule
	err := row.Scan(&schedule.ID, &schedule.GuildID, &schedule.ScheduledAt, &schedule.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get roulette schedule: %w", err)
	}
	return &schedule, nil
}

// GetScheduledPairings returns all scheduled pairings that are due to be executed
func (db *DB) GetScheduledPairings() ([]RouletteSchedule, error) {
	query := `
	SELECT id, guild_id, scheduled_at, created_at
	FROM roulette_schedules
	WHERE scheduled_at <= datetime('now')
	ORDER BY scheduled_at ASC
	`
	rows, err := db.conn.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to get scheduled pairings: %w", err)
	}
	defer rows.Close()

	var schedules []RouletteSchedule
	for rows.Next() {
		var schedule RouletteSchedule
		err := rows.Scan(&schedule.ID, &schedule.GuildID, &schedule.ScheduledAt, &schedule.CreatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan scheduled pairing: %w", err)
		}
		schedules = append(schedules, schedule)
	}
	return schedules, nil
}

// ClearRouletteSchedule removes the scheduled pairing for a guild
func (db *DB) ClearRouletteSchedule(guildID string) error {
	query := `DELETE FROM roulette_schedules WHERE guild_id = ?`
	_, err := db.conn.Exec(query, guildID)
	if err != nil {
		return fmt.Errorf("failed to clear roulette schedule: %w", err)
	}
	return nil
}
