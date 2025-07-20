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

// UserData represents a simple data structure for storing user information
type UserData struct {
	ID        int       `json:"id"`
	UserID    string    `json:"user_id"`
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
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
	CREATE TABLE IF NOT EXISTS user_data (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		user_id TEXT NOT NULL,
		key TEXT NOT NULL,
		value TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		UNIQUE(user_id, key)
	);

	CREATE INDEX IF NOT EXISTS idx_user_data_user_id ON user_data(user_id);
	CREATE INDEX IF NOT EXISTS idx_user_data_key ON user_data(key);
	`

	_, err := db.conn.Exec(query)
	return err
}

// StoreUserData stores or updates user data
func (db *DB) StoreUserData(userID, key, value string) error {
	query := `
	INSERT INTO user_data (user_id, key, value, updated_at)
	VALUES (?, ?, ?, CURRENT_TIMESTAMP)
	ON CONFLICT(user_id, key) 
	DO UPDATE SET 
		value = excluded.value,
		updated_at = CURRENT_TIMESTAMP
	`

	_, err := db.conn.Exec(query, userID, key, value)
	if err != nil {
		return fmt.Errorf("failed to store user data: %w", err)
	}

	return nil
}

// GetUserData retrieves user data by user ID and key
func (db *DB) GetUserData(userID, key string) (*UserData, error) {
	query := `
	SELECT id, user_id, key, value, created_at, updated_at
	FROM user_data
	WHERE user_id = ? AND key = ?
	`

	row := db.conn.QueryRow(query, userID, key)

	var data UserData
	err := row.Scan(&data.ID, &data.UserID, &data.Key, &data.Value, &data.CreatedAt, &data.UpdatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil // No data found
		}
		return nil, fmt.Errorf("failed to get user data: %w", err)
	}

	return &data, nil
}

// GetAllUserData retrieves all data for a specific user
func (db *DB) GetAllUserData(userID string) ([]UserData, error) {
	query := `
	SELECT id, user_id, key, value, created_at, updated_at
	FROM user_data
	WHERE user_id = ?
	ORDER BY key
	`

	rows, err := db.conn.Query(query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query user data: %w", err)
	}
	defer rows.Close()

	var dataList []UserData
	for rows.Next() {
		var data UserData
		err := rows.Scan(&data.ID, &data.UserID, &data.Key, &data.Value, &data.CreatedAt, &data.UpdatedAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan user data: %w", err)
		}
		dataList = append(dataList, data)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating user data: %w", err)
	}

	return dataList, nil
}

// DeleteUserData deletes user data by user ID and key
func (db *DB) DeleteUserData(userID, key string) error {
	query := `DELETE FROM user_data WHERE user_id = ? AND key = ?`

	result, err := db.conn.Exec(query, userID, key)
	if err != nil {
		return fmt.Errorf("failed to delete user data: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no data found to delete")
	}

	return nil
}

// GetStats returns some basic statistics about the database
func (db *DB) GetStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Count total records
	var totalRecords int
	err := db.conn.QueryRow("SELECT COUNT(*) FROM user_data").Scan(&totalRecords)
	if err != nil {
		return nil, fmt.Errorf("failed to count total records: %w", err)
	}
	stats["total_records"] = totalRecords

	// Count unique users
	var uniqueUsers int
	err = db.conn.QueryRow("SELECT COUNT(DISTINCT user_id) FROM user_data").Scan(&uniqueUsers)
	if err != nil {
		return nil, fmt.Errorf("failed to count unique users: %w", err)
	}
	stats["unique_users"] = uniqueUsers

	// Get most recent entry
	var lastUpdated sql.NullString
	err = db.conn.QueryRow("SELECT MAX(updated_at) FROM user_data").Scan(&lastUpdated)
	if err != nil {
		return nil, fmt.Errorf("failed to get last updated: %w", err)
	}
	if lastUpdated.Valid {
		stats["last_updated"] = lastUpdated.String
	} else {
		stats["last_updated"] = "No data"
	}

	return stats, nil
}
