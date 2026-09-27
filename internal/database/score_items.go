package database

import (
	"database/sql"
	"fmt"
	"strings"
)

// ScoreItem is one thing a user holds in a guild. Count is nil for uncounted
// things (e.g. "a can of eggs"), which are displayed exactly as named.
type ScoreItem struct {
	Name  string
	Count *int64
}

// NormalizeScoreItemName collapses whitespace so display names are tidy and
// stacking is not defeated by stray spaces.
func NormalizeScoreItemName(name string) string {
	return strings.Join(strings.Fields(name), " ")
}

// scoreItemKey is the stacking key: gives stack when names match
// case-insensitively. Computed in Go because SQLite's lower() is ASCII-only.
func scoreItemKey(name string) string {
	return strings.ToLower(NormalizeScoreItemName(name))
}

// GiveScoreItem records a thing given to a user. A counted give adds to any
// existing count for the same name; an uncounted give only creates the entry if
// it does not already exist. The first-given spelling of the name is kept.
func (db *DB) GiveScoreItem(guildID, userID, name string, count *int64) error {
	name = NormalizeScoreItemName(name)
	if name == "" {
		return fmt.Errorf("score item name is empty")
	}
	if count != nil && *count < 1 {
		return fmt.Errorf("score item count must be positive, got %d", *count)
	}

	var countArg sql.NullInt64
	if count != nil {
		countArg = sql.NullInt64{Int64: *count, Valid: true}
	}

	_, err := db.conn.Exec(`
		INSERT INTO score_items (guild_id, user_id, name, name_key, count)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT (guild_id, user_id, name_key) DO UPDATE SET
			count = CASE
				WHEN excluded.count IS NULL THEN score_items.count
				ELSE COALESCE(score_items.count, 0) + excluded.count
			END,
			updated_at = CURRENT_TIMESTAMP
	`, guildID, userID, name, scoreItemKey(name), countArg)
	if err != nil {
		return fmt.Errorf("failed to give score item: %w", err)
	}
	return nil
}

// GetScoreItems returns everything a user holds in a guild, in the order the
// things were first given.
func (db *DB) GetScoreItems(guildID, userID string) ([]ScoreItem, error) {
	rows, err := db.conn.Query(`
		SELECT name, count FROM score_items
		WHERE guild_id = ? AND user_id = ?
		ORDER BY id ASC
	`, guildID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get score items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var items []ScoreItem
	for rows.Next() {
		var item ScoreItem
		var count sql.NullInt64
		if err := rows.Scan(&item.Name, &count); err != nil {
			return nil, fmt.Errorf("failed to scan score item: %w", err)
		}
		if count.Valid {
			c := count.Int64
			item.Count = &c
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate score items: %w", err)
	}
	return items, nil
}
