package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

// ScoreItem is one thing a user holds in a guild. Count is nil for uncounted
// things (e.g. "a can of eggs"), which are displayed exactly as named.
type ScoreItem struct {
	Name  string
	Count *int64
}

// GiveResult reports what GiveScoreItem did.
type GiveResult int

const (
	// GiveApplied means the thing was added or its count increased.
	GiveApplied GiveResult = iota
	// GiveAlreadyHeld means an uncounted thing was given to a user who already
	// has it; nothing changed.
	GiveAlreadyHeld
	// GiveKindMismatch means the give was counted and the held thing is not (or
	// vice versa); nothing changed.
	GiveKindMismatch
)

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

// GiveScoreItem records a thing given to a user. A counted give adds to an
// existing counted thing of the same name. An uncounted give of a thing the
// user already holds, or a give whose countedness differs from the held thing,
// changes nothing and is reported through the result. The first-given
// spelling of the name is kept.
func (db *DB) GiveScoreItem(guildID, userID, name string, count *int64) (GiveResult, error) {
	name = NormalizeScoreItemName(name)
	if name == "" {
		return 0, fmt.Errorf("score item name is empty")
	}
	if count != nil && *count < 1 {
		return 0, fmt.Errorf("score item count must be positive, got %d", *count)
	}
	key := scoreItemKey(name)

	tx, err := db.conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin give transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var existing sql.NullInt64
	err = tx.QueryRow(
		`SELECT count FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
		guildID, userID, key,
	).Scan(&existing)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		var countArg sql.NullInt64
		if count != nil {
			countArg = sql.NullInt64{Int64: *count, Valid: true}
		}
		if _, err := tx.Exec(
			`INSERT INTO score_items (guild_id, user_id, name, name_key, count) VALUES (?, ?, ?, ?, ?)`,
			guildID, userID, name, key, countArg,
		); err != nil {
			return 0, fmt.Errorf("failed to insert score item: %w", err)
		}
	case err != nil:
		return 0, fmt.Errorf("failed to look up score item: %w", err)
	case existing.Valid != (count != nil):
		return GiveKindMismatch, nil
	case count == nil:
		return GiveAlreadyHeld, nil
	default:
		if _, err := tx.Exec(
			`UPDATE score_items SET count = count + ?, updated_at = CURRENT_TIMESTAMP
			 WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
			*count, guildID, userID, key,
		); err != nil {
			return 0, fmt.Errorf("failed to update score item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit give: %w", err)
	}
	return GiveApplied, nil
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
