package database

import (
	"database/sql"
	"errors"
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

// SnowballScore represents a user's cumulative snowball score
type SnowballScore struct {
	UserID  string `json:"user_id"`
	GuildID string `json:"guild_id"`
	Score   int    `json:"score"`
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


	CREATE TABLE IF NOT EXISTS welcome_messages (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    user_id TEXT NOT NULL,
	    message TEXT NOT NULL,
	    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_welcome_messages_user_id ON welcome_messages(user_id);

	CREATE TABLE IF NOT EXISTS snowball_scores (
		user_id TEXT NOT NULL,
		guild_id TEXT NOT NULL,
		score INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY (user_id, guild_id)
	);

	CREATE INDEX IF NOT EXISTS idx_snowball_scores_guild_id ON snowball_scores(guild_id);

	CREATE TABLE IF NOT EXISTS intro_feed_posts (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		thread_id TEXT NOT NULL,
		feed_message_id TEXT,
		is_bump BOOLEAN NOT NULL DEFAULT 0,
		posted_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_intro_feed_posts_user_id ON intro_feed_posts(user_id);
	CREATE INDEX IF NOT EXISTS idx_intro_feed_posts_posted_at ON intro_feed_posts(posted_at);
	`

	_, err := db.conn.Exec(query)
	if err != nil {
		return err
	}

	// One-time migration: recreate intro_feed_posts if it has the old schema
	// (missing is_bump column due to UNIQUE(thread_id) constraint).
	var hasIsBump bool
	rows, err := db.conn.Query(`PRAGMA table_info(intro_feed_posts)`)
	if err != nil {
		return fmt.Errorf("failed to check intro_feed_posts schema: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull int
		var dfltValue *string
		var pk int
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dfltValue, &pk); err != nil {
			return fmt.Errorf("failed to scan table_info: %w", err)
		}
		if name == "is_bump" {
			hasIsBump = true
			break
		}
	}
	if !hasIsBump {
		// Old schema: drop and let next startup recreate with new schema
		if _, err := db.conn.Exec(`DROP TABLE intro_feed_posts`); err != nil {
			return fmt.Errorf("failed to drop old intro_feed_posts table: %w", err)
		}
		if _, err := db.conn.Exec(`
			CREATE TABLE intro_feed_posts (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				user_id TEXT NOT NULL,
				thread_id TEXT NOT NULL,
				feed_message_id TEXT,
				is_bump BOOLEAN NOT NULL DEFAULT 0,
				posted_at DATETIME DEFAULT CURRENT_TIMESTAMP
			)`); err != nil {
			return fmt.Errorf("failed to recreate intro_feed_posts table: %w", err)
		}
	}

	return nil
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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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
	defer func() { _ = rows.Close() }()

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

func (db *DB) SetWelcomeMessage(userId string, message string) error {
	currentMsg, err := db.GetWelcomeMessage()
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("failed to add welcome message: %w", err)
	}

	var query string
	if len(currentMsg) <= 0 {
		query = `INSERT INTO welcome_messages (user_id, message) VALUES (?, ?)`
	} else {
		query = `UPDATE welcome_messages SET user_id = ?, message = ?`
	}
	_, err = db.conn.Exec(query, userId, message) // Fixed parameter order
	if err != nil {
		return fmt.Errorf("failed to add welcome message: %w", err)
	}

	return nil
}

func (db *DB) GetWelcomeMessage() (string, error) {
	query := `SELECT message FROM welcome_messages ORDER BY id DESC LIMIT 1`
	var message string

	err := db.conn.QueryRow(query).Scan(&message)
	if err != nil {
		return "", fmt.Errorf("failed to get welcome message: %w", err)
	}

	return message, nil
}

// AddSnowballScore increments a user's snowball score for a guild
func (db *DB) AddSnowballScore(userID, guildID string, delta int) error {
	if delta == 0 {
		return nil
	}

	query := `
	INSERT INTO snowball_scores (user_id, guild_id, score)
	VALUES (?, ?, ?)
	ON CONFLICT(user_id, guild_id) DO UPDATE SET score = score + excluded.score
	`
	_, err := db.conn.Exec(query, userID, guildID, delta)
	if err != nil {
		return fmt.Errorf("failed to add snowball score: %w", err)
	}
	return nil
}

// GetTopSnowballScores returns the top N snowball scores for a guild
func (db *DB) GetTopSnowballScores(guildID string, limit int) ([]SnowballScore, error) {
	if limit <= 0 {
		limit = 20
	}

	query := `
	SELECT user_id, guild_id, score
	FROM snowball_scores
	WHERE guild_id = ?
	ORDER BY score DESC
	LIMIT ?
	`
	rows, err := db.conn.Query(query, guildID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get snowball scores: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var scores []SnowballScore
	for rows.Next() {
		var s SnowballScore
		if err := rows.Scan(&s.UserID, &s.GuildID, &s.Score); err != nil {
			return nil, fmt.Errorf("failed to scan snowball score: %w", err)
		}
		scores = append(scores, s)
	}
	return scores, nil
}

// ClearSnowballScores removes all snowball scores for a guild
func (db *DB) ClearSnowballScores(guildID string) error {
	query := `DELETE FROM snowball_scores WHERE guild_id = ?`
	_, err := db.conn.Exec(query, guildID)
	if err != nil {
		return fmt.Errorf("failed to clear snowball scores: %w", err)
	}
	return nil
}

// Intro Feed methods

// IntroFeedPost represents a record of an intro post being forwarded to the feed channel
type IntroFeedPost struct {
	ID            int       `json:"id"`
	UserID        string    `json:"user_id"`
	ThreadID      string    `json:"thread_id"`
	FeedMessageID string    `json:"feed_message_id"`
	IsBump        bool      `json:"is_bump"`
	PostedAt      time.Time `json:"posted_at"`
}

// RecordIntroFeedPost records that a user's intro was posted or bumped to the feed channel.
func (db *DB) RecordIntroFeedPost(userID, threadID, feedMessageID string, isBump bool) error {
	query := `
	INSERT INTO intro_feed_posts (user_id, thread_id, feed_message_id, is_bump, posted_at)
	VALUES (?, ?, ?, ?, CURRENT_TIMESTAMP)
	`
	_, err := db.conn.Exec(query, userID, threadID, feedMessageID, isBump)
	if err != nil {
		return fmt.Errorf("failed to record intro feed post: %w", err)
	}
	return nil
}

// GetLastIntroFeedPostTime returns the most recent time a user had their intro posted to the feed.
// Returns zero time if no record exists.
func (db *DB) GetLastIntroFeedPostTime(userID string) (time.Time, error) {
	query := `SELECT posted_at FROM intro_feed_posts WHERE user_id = ? ORDER BY posted_at DESC LIMIT 1`
	var postedAt time.Time
	err := db.conn.QueryRow(query, userID).Scan(&postedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get last intro feed post time: %w", err)
	}
	return postedAt, nil
}

// IsUserEligibleForIntroFeed checks if enough time has passed since the user's last feed post.
// Returns true if the user is eligible, false if they're still in the cooldown period.
// Also returns the time remaining until they're eligible (zero if eligible).
func (db *DB) IsUserEligibleForIntroFeed(userID string, cooldownHours int) (bool, time.Duration, error) {
	lastPost, err := db.GetLastIntroFeedPostTime(userID)
	if err != nil {
		return false, 0, err
	}
	if lastPost.IsZero() {
		return true, 0, nil // Never posted before, eligible
	}

	cooldown := time.Duration(cooldownHours) * time.Hour
	eligibleAt := lastPost.Add(cooldown)
	now := time.Now()

	if now.After(eligibleAt) {
		return true, 0, nil
	}
	return false, eligibleAt.Sub(now), nil
}

// GetUserIntroPostCount returns the number of times a user has posted (not bumped) to the intro feed.
func (db *DB) GetUserIntroPostCount(userID string) (int, error) {
	query := `SELECT COUNT(*) FROM intro_feed_posts WHERE user_id = ? AND is_bump = 0`
	var count int
	err := db.conn.QueryRow(query, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to get user intro post count: %w", err)
	}
	return count, nil
}
