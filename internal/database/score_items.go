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
// the user had (0 when not held).
func (db *DB) TakeScoreItem(guildID, userID, name string, count int64) (result TakeResult, held int64, err error) {
	name, err = validateScoreItem(name, count)
	if err != nil {
		return 0, 0, err
	}
	return db.takeScoreItem(guildID, userID, name, count)
}

// TakeAllScoreItem removes a thing from a user entirely; held is the amount
// the user had (0 when not held).
func (db *DB) TakeAllScoreItem(guildID, userID, name string) (result TakeResult, held int64, err error) {
	name, err = validateScoreItem(name, 1)
	if err != nil {
		return 0, 0, err
	}
	return db.takeScoreItem(guildID, userID, name, 0)
}

// takeScoreItem removes count of a thing, or all of it when count is 0.
func (db *DB) takeScoreItem(guildID, userID, name string, count int64) (result TakeResult, held int64, err error) {
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
	case count == 0 || held == count:
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

// maxScoreItemSuggestions is Discord's cap on autocomplete choices.
const maxScoreItemSuggestions = 25

// SuggestScoreItemNames returns names of things currently held in a guild
// whose name contains query (case-insensitively), for autocomplete. When
// userID is set only that user's things are considered. Each thing appears
// once, spelled as it was first given. Things nobody holds any more are gone
// from the table, so they are never suggested.
func (db *DB) SuggestScoreItemNames(guildID, userID, query string) ([]string, error) {
	sqlQuery := `
		SELECT name, MIN(id) AS first_id FROM score_items
		WHERE guild_id = ? AND instr(name_key, ?) > 0`
	args := []any{guildID, scoreItemKey(query)}
	if userID != "" {
		sqlQuery += ` AND user_id = ?`
		args = append(args, userID)
	}
	// SQLite returns the bare name column from the MIN(id) row.
	sqlQuery += ` GROUP BY name_key ORDER BY name_key LIMIT ?`
	args = append(args, maxScoreItemSuggestions)

	rows, err := db.conn.Query(sqlQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to suggest score items: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var names []string
	for rows.Next() {
		var name string
		var firstID int64
		if err := rows.Scan(&name, &firstID); err != nil {
			return nil, fmt.Errorf("failed to scan score item suggestion: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate score item suggestions: %w", err)
	}
	return names, nil
}

// ScoreLeaderboardEntry is one user's holding of a thing.
type ScoreLeaderboardEntry struct {
	UserID string
	Count  int64
}

// GetScoreLeaderboard returns the users holding the most of a thing in a
// guild, highest first, capped at limit; ties go to whoever got it first.
// name is the thing's first-given spelling, or "" when nobody holds it.
func (db *DB) GetScoreLeaderboard(guildID, thing string, limit int) (name string, entries []ScoreLeaderboardEntry, err error) {
	key := scoreItemKey(thing)
	err = db.conn.QueryRow(
		`SELECT name FROM score_items WHERE guild_id = ? AND name_key = ? ORDER BY id ASC LIMIT 1`,
		guildID, key,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to look up score leaderboard thing: %w", err)
	}

	rows, err := db.conn.Query(`
		SELECT user_id, count FROM score_items
		WHERE guild_id = ? AND name_key = ?
		ORDER BY count DESC, id ASC
		LIMIT ?
	`, guildID, key, limit)
	if err != nil {
		return "", nil, fmt.Errorf("failed to get score leaderboard: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var e ScoreLeaderboardEntry
		if err := rows.Scan(&e.UserID, &e.Count); err != nil {
			return "", nil, fmt.Errorf("failed to scan score leaderboard: %w", err)
		}
		entries = append(entries, e)
	}
	if err := rows.Err(); err != nil {
		return "", nil, fmt.Errorf("failed to iterate score leaderboard: %w", err)
	}
	return name, entries, nil
}
