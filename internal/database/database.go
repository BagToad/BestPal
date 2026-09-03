package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// DB wraps the SQL database connection
type DB struct {
	conn *sql.DB
}

// buildDSN augments a SQLite file path with connection parameters that let the
// database work on network-backed volumes (Azure Files / SMB), where the POSIX
// byte-range locking SQLite uses by default is unavailable and every write
// otherwise fails with "database is locked". unix-dotfile locking uses a
// companion lock file instead, and a busy timeout absorbs brief contention.
// In-memory databases are returned unchanged.
func buildDSN(dbPath string) string {
	if dbPath == "" || dbPath == ":memory:" || strings.HasPrefix(dbPath, "file::memory:") {
		return dbPath
	}
	sep := "?"
	if strings.ContainsRune(dbPath, '?') {
		sep = "&"
	}
	return dbPath + sep + "vfs=unix-dotfile&_busy_timeout=5000"
}

// NewDB creates a new database connection and initializes tables
func NewDB(dbPath string) (*DB, error) {
	conn, err := sql.Open("sqlite3", buildDSN(dbPath))
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// SQLite allows only one writer. Serializing access through a single
	// connection avoids intra-process "database is locked" contention, which
	// matters most on network-backed volumes where the dot-file locking from
	// buildDSN is coarse. It also keeps in-memory test databases consistent,
	// since each sqlite3 connection to ":memory:" is otherwise distinct.
	conn.SetMaxOpenConns(1)

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
	CREATE TABLE IF NOT EXISTS welcome_messages (
	    id INTEGER PRIMARY KEY AUTOINCREMENT,
	    user_id TEXT NOT NULL,
	    message TEXT NOT NULL,
	    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);
	
	CREATE INDEX IF NOT EXISTS idx_welcome_messages_user_id ON welcome_messages(user_id);

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

	CREATE TABLE IF NOT EXISTS GameThreadsLookupExecutionTracker (
		intro_thread_id TEXT PRIMARY KEY,
		last_executed_at DATETIME NOT NULL
	);

	CREATE TABLE IF NOT EXISTS introduction_threads (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		thread_id TEXT NOT NULL UNIQUE,
		user_id TEXT NOT NULL,
		username TEXT,
		thread_title TEXT,
		first_message_content TEXT,
		applied_tags TEXT DEFAULT '[]',
		created_at DATETIME,
		fetched_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_introduction_threads_user_id ON introduction_threads(user_id);
	CREATE INDEX IF NOT EXISTS idx_introduction_threads_fetched_at ON introduction_threads(fetched_at);

	CREATE TABLE IF NOT EXISTS scam_image_hashes (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		hash TEXT NOT NULL UNIQUE,
		added_by TEXT,
		source TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_scam_image_hashes_hash ON scam_image_hashes(hash);

	CREATE TABLE IF NOT EXISTS guild_config (
		guild_id   TEXT NOT NULL,
		key        TEXT NOT NULL,
		value      TEXT NOT NULL,
		updated_by TEXT,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		PRIMARY KEY (guild_id, key)
	);
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
		if _, err := db.conn.Exec(`
			CREATE INDEX IF NOT EXISTS idx_intro_feed_posts_user_id ON intro_feed_posts(user_id);
			CREATE INDEX IF NOT EXISTS idx_intro_feed_posts_posted_at ON intro_feed_posts(posted_at);
		`); err != nil {
			return fmt.Errorf("failed to recreate intro_feed_posts indexes: %w", err)
		}
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
	query := `SELECT posted_at FROM intro_feed_posts WHERE user_id = ? AND feed_message_id != '' ORDER BY posted_at DESC LIMIT 1`
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

// GetRecentIntroFeedPosts returns all intro feed posts since the given time.
func (db *DB) GetRecentIntroFeedPosts(since time.Time) ([]IntroFeedPost, error) {
	query := `
	SELECT id, user_id, thread_id, feed_message_id, is_bump, posted_at
	FROM intro_feed_posts
	WHERE posted_at >= ?
	ORDER BY posted_at ASC
	`
	// Format to match SQLite's CURRENT_TIMESTAMP format for reliable comparison
	sinceStr := since.UTC().Format("2006-01-02 15:04:05")
	rows, err := db.conn.Query(query, sinceStr)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent intro feed posts: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var posts []IntroFeedPost
	for rows.Next() {
		var p IntroFeedPost
		if err := rows.Scan(&p.ID, &p.UserID, &p.ThreadID, &p.FeedMessageID, &p.IsBump, &p.PostedAt); err != nil {
			return nil, fmt.Errorf("failed to scan intro feed post: %w", err)
		}
		posts = append(posts, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate intro feed posts: %w", err)
	}
	return posts, nil
}

// IsIntroEligibleForGameThreadsLookup reports whether an intro thread can run
// the lookup action again based on the last successful execution timestamp and
// the intro post's edited timestamp. The lookup is eligible when there is no
// previous execution, or when the latest execution is strictly before the most
// recent edit timestamp.
func (db *DB) IsIntroEligibleForGameThreadsLookup(introID string, introEditedAt time.Time) (bool, time.Duration, error) {
	if strings.TrimSpace(introID) == "" {
		return false, 0, fmt.Errorf("intro id is required")
	}

	var lastExecutedAt time.Time
	err := db.conn.QueryRow(`
		SELECT last_executed_at
		FROM GameThreadsLookupExecutionTracker
		WHERE intro_thread_id = ?
	`, introID).Scan(&lastExecutedAt)

	// If no record exists, this means the lookup has never been executed for this intro thread, so it's eligible.
	if errors.Is(err, sql.ErrNoRows) {
		return true, 0, nil
	}
	if err != nil {
		return false, 0, fmt.Errorf("failed to read game threads lookup execution tracker: %w", err)
	}

	if introEditedAt.IsZero() {
		return false, 0, nil
	}
	if lastExecutedAt.Before(introEditedAt) {
		return true, 0, nil
	}

	pending := lastExecutedAt.Sub(introEditedAt)
	if pending < 0 {
		pending = 0
	}
	return false, pending, nil
}

// UpsertGameThreadsLookupExecution records the latest successful lookup
// execution for an intro thread.
func (db *DB) UpsertGameThreadsLookupExecution(introThreadID string) error {
	if strings.TrimSpace(introThreadID) == "" {
		return fmt.Errorf("intro thread id is required")
	}

	_, err := db.conn.Exec(`
		INSERT INTO GameThreadsLookupExecutionTracker (intro_thread_id, last_executed_at)
		VALUES (?, CURRENT_TIMESTAMP)
		ON CONFLICT(intro_thread_id) DO UPDATE SET
			last_executed_at = CURRENT_TIMESTAMP
	`, introThreadID)
	if err != nil {
		return fmt.Errorf("failed to upsert game threads lookup execution tracker: %w", err)
	}
	return nil
}

// IntroductionThread stores full thread content for analysis
type IntroductionThread struct {
	ID                  int       `json:"id"`
	ThreadID            string    `json:"thread_id"`
	UserID              string    `json:"user_id"`
	Username            string    `json:"username"`
	ThreadTitle         string    `json:"thread_title"`
	FirstMessageContent string    `json:"first_message_content"`
	AppliedTags         string    `json:"applied_tags"` // JSON array of tag IDs
	CreatedAt           time.Time `json:"created_at"`
	FetchedAt           time.Time `json:"fetched_at"`
}

// SaveIntroductionThread stores a full introduction thread
func (db *DB) SaveIntroductionThread(thread *IntroductionThread) error {
	query := `
	INSERT INTO introduction_threads (thread_id, user_id, username, thread_title, first_message_content, applied_tags, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(thread_id) DO UPDATE SET
		user_id = excluded.user_id,
		username = excluded.username,
		thread_title = excluded.thread_title,
		first_message_content = excluded.first_message_content,
		applied_tags = excluded.applied_tags,
		fetched_at = CURRENT_TIMESTAMP
	`
	_, err := db.conn.Exec(query,
		thread.ThreadID,
		thread.UserID,
		thread.Username,
		thread.ThreadTitle,
		thread.FirstMessageContent,
		thread.AppliedTags,
		thread.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("failed to save introduction thread: %w", err)
	}
	return nil
}

// Scam image hash methods (scamguard module)

// ScamImageHash is a known-bad perceptual image hash used by the scamguard
// module to detect repeated scam images.
type ScamImageHash struct {
	ID        int       `json:"id"`
	Hash      string    `json:"hash"`
	AddedBy   string    `json:"added_by"`
	Source    string    `json:"source"`
	CreatedAt time.Time `json:"created_at"`
}

// AddScamImageHash inserts a known-bad image hash. Returns true if the hash was
// newly inserted, false if it already existed. hash is the goimagehash string
// form (e.g. "p:ff00..."); source is typically "seed" or "command".
func (db *DB) AddScamImageHash(hash, addedBy, source string) (bool, error) {
	res, err := db.conn.Exec(`
	INSERT INTO scam_image_hashes (hash, added_by, source)
	VALUES (?, ?, ?)
	ON CONFLICT(hash) DO NOTHING
	`, hash, addedBy, source)
	if err != nil {
		return false, fmt.Errorf("failed to add scam image hash: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read rows affected: %w", err)
	}
	return affected > 0, nil
}

// GetScamImageHashes returns all known-bad image hashes, oldest first.
func (db *DB) GetScamImageHashes() ([]ScamImageHash, error) {
	rows, err := db.conn.Query(`
	SELECT id, hash, COALESCE(added_by, ''), COALESCE(source, ''), created_at
	FROM scam_image_hashes
	ORDER BY id
	`)
	if err != nil {
		return nil, fmt.Errorf("failed to get scam image hashes: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var hashes []ScamImageHash
	for rows.Next() {
		var h ScamImageHash
		if err := rows.Scan(&h.ID, &h.Hash, &h.AddedBy, &h.Source, &h.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan scam image hash: %w", err)
		}
		hashes = append(hashes, h)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate scam image hashes: %w", err)
	}
	return hashes, nil
}

// RemoveScamImageHash deletes a known-bad image hash by its string form. It
// returns true when a row was actually deleted, false when the hash was absent.
func (db *DB) RemoveScamImageHash(hash string) (bool, error) {
	res, err := db.conn.Exec(`DELETE FROM scam_image_hashes WHERE hash = ?`, hash)
	if err != nil {
		return false, fmt.Errorf("failed to remove scam image hash: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("failed to read rows affected: %w", err)
	}
	return affected > 0, nil
}
