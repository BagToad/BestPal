package database

import (
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
)

// ScoreItem is one thing a user holds in a guild.
type ScoreItem struct {
	Name  string
	Count int64
}

// GiveResult reports what GiveScoreItem did.
type GiveResult int

const (
	// GiveApplied means the thing was added or its count increased.
	GiveApplied GiveResult = iota
	// GiveOverflow means the new total would not fit in an int64; nothing changed.
	GiveOverflow
)

// TakeResult reports what TakeScoreItem did.
type TakeResult int

const (
	// TakeApplied means the count was reduced (and the thing removed at zero).
	TakeApplied TakeResult = iota
	// TakeNotHeld means the user doesn't have the thing; nothing changed.
	TakeNotHeld
	// TakeInsufficient means the user has fewer than requested; nothing changed.
	TakeInsufficient
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

func validateScoreItem(name string, count int64) (string, error) {
	name = NormalizeScoreItemName(name)
	if name == "" {
		return "", fmt.Errorf("score item name is empty")
	}
	if count < 1 {
		return "", fmt.Errorf("score item count must be positive, got %d", count)
	}
	return name, nil
}

// GiveScoreItem adds count of a thing to a user, stacking onto an existing
// thing of the same name. The first-given spelling of the name is kept.
func (db *DB) GiveScoreItem(guildID, userID, name string, count int64) (GiveResult, error) {
	name, err := validateScoreItem(name, count)
	if err != nil {
		return 0, err
	}
	key := scoreItemKey(name)

	tx, err := db.conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("failed to begin give transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var held int64
	err = tx.QueryRow(
		`SELECT count FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
		guildID, userID, key,
	).Scan(&held)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if _, err := tx.Exec(
			`INSERT INTO score_items (guild_id, user_id, name, name_key, count) VALUES (?, ?, ?, ?, ?)`,
			guildID, userID, name, key, count,
		); err != nil {
			return 0, fmt.Errorf("failed to insert score item: %w", err)
		}
	case err != nil:
		return 0, fmt.Errorf("failed to look up score item: %w", err)
	case held > math.MaxInt64-count:
		return GiveOverflow, nil
	default:
		if _, err := tx.Exec(
			`UPDATE score_items SET count = count + ?, updated_at = CURRENT_TIMESTAMP
			 WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
			count, guildID, userID, key,
		); err != nil {
			return 0, fmt.Errorf("failed to update score item: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("failed to commit give: %w", err)
	}
	return GiveApplied, nil
}

// TakeScoreItem removes count of a thing from a user, deleting it when none
// are left. Taking more than the user has changes nothing; held is the amount
// the user has (0 when not held).
func (db *DB) TakeScoreItem(guildID, userID, name string, count int64) (result TakeResult, held int64, err error) {
	name, err = validateScoreItem(name, count)
	if err != nil {
		return 0, 0, err
	}
	key := scoreItemKey(name)

	tx, err := db.conn.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin take transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	err = tx.QueryRow(
		`SELECT count FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
		guildID, userID, key,
	).Scan(&held)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return TakeNotHeld, 0, nil
	case err != nil:
		return 0, 0, fmt.Errorf("failed to look up score item: %w", err)
	case held < count:
		return TakeInsufficient, held, nil
	case held == count:
		_, err = tx.Exec(
			`DELETE FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
			guildID, userID, key,
		)
	default:
		_, err = tx.Exec(
			`UPDATE score_items SET count = count - ?, updated_at = CURRENT_TIMESTAMP
			 WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
			count, guildID, userID, key,
		)
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to update score item: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit take: %w", err)
	}
	return TakeApplied, held, nil
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
		if err := rows.Scan(&item.Name, &item.Count); err != nil {
			return nil, fmt.Errorf("failed to scan score item: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate score items: %w", err)
	}
	return items, nil
}
