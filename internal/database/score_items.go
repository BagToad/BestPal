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

// GetScoreLeaderboard returns the members holding the most of a thing in a
// guild, highest first, capped at limit; ties go to whoever got it first.
// name is the thing's first-given
// spelling, or "" when nobody holds it.
func (db *DB) GetScoreLeaderboard(guildID, thing string, limit int) (name string, entries []ScoreLeaderboardEntry, err error) {
	key := scoreItemKey(thing)
	err = db.conn.QueryRow(
		`SELECT name FROM score_items s WHERE guild_id = ? AND name_key = ? ORDER BY id ASC LIMIT 1`,
		guildID, key,
	).Scan(&name)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil, nil
	}
	if err != nil {
		return "", nil, fmt.Errorf("failed to look up score leaderboard thing: %w", err)
	}

	rows, err := db.conn.Query(`
		SELECT user_id, count FROM score_items s
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

// GetScoreRank returns how many of a thing a user holds and their leaderboard
// rank: one more than the number of members who hold
// strictly more, so ties share a rank. count is 0 when the user holds none.
func (db *DB) GetScoreRank(guildID, thing, userID string) (count int64, rank int, err error) {
	key := scoreItemKey(thing)
	err = db.conn.QueryRow(
		`SELECT count FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
		guildID, userID, key,
	).Scan(&count)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, fmt.Errorf("failed to look up score rank: %w", err)
	}
	err = db.conn.QueryRow(
		`SELECT COUNT(*) + 1 FROM score_items s WHERE guild_id = ? AND name_key = ? AND count > ?`,
		guildID, key, count,
	).Scan(&rank)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to compute score rank: %w", err)
	}
	return count, rank, nil
}

// PurgeScoreMember deletes everything a user holds in a guild, e.g. after they
// leave it.
func (db *DB) PurgeScoreMember(guildID, userID string) error {
	if _, err := db.conn.Exec(
		`DELETE FROM score_items WHERE guild_id = ? AND user_id = ?`,
		guildID, userID,
	); err != nil {
		return fmt.Errorf("failed to purge score member: %w", err)
	}
	return nil
}

// RenameResult reports what RenameScoreItem did.
type RenameResult int

const (
	// RenameApplied means the thing was renamed for everyone holding it.
	RenameApplied RenameResult = iota
	// RenameNotHeld means nobody holds the thing; nothing changed.
	RenameNotHeld
	// RenameOverflow means merging into an existing thing would push someone's
	// total past an int64; nothing changed.
	RenameOverflow
)

// RenameScoreItem renames a thing for everyone in a guild. Holders who
// already have the new thing get the counts merged. The new spelling becomes
// the display name for everyone holding it. people is how many users held the
// old thing.
func (db *DB) RenameScoreItem(guildID, from, to string) (result RenameResult, people int, err error) {
	if from, err = validateScoreItem(from, 1); err != nil {
		return 0, 0, err
	}
	if to, err = validateScoreItem(to, 1); err != nil {
		return 0, 0, err
	}
	fromKey, toKey := scoreItemKey(from), scoreItemKey(to)

	tx, err := db.conn.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin rename transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	type holding struct {
		userID string
		count  int64
	}
	rows, err := tx.Query(
		`SELECT user_id, count FROM score_items WHERE guild_id = ? AND name_key = ?`,
		guildID, fromKey,
	)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to look up score items to rename: %w", err)
	}
	var held []holding
	for rows.Next() {
		var h holding
		if err := rows.Scan(&h.userID, &h.count); err != nil {
			_ = rows.Close()
			return 0, 0, fmt.Errorf("failed to scan score item to rename: %w", err)
		}
		held = append(held, h)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, fmt.Errorf("failed to iterate score items to rename: %w", err)
	}
	if len(held) == 0 {
		return RenameNotHeld, 0, nil
	}

	if fromKey != toKey {
		for _, h := range held {
			var existing int64
			err := tx.QueryRow(
				`SELECT count FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
				guildID, h.userID, toKey,
			).Scan(&existing)
			switch {
			case errors.Is(err, sql.ErrNoRows):
				_, err = tx.Exec(
					`UPDATE score_items SET name_key = ?, updated_at = CURRENT_TIMESTAMP
					 WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
					toKey, guildID, h.userID, fromKey,
				)
			case err != nil:
				return 0, 0, fmt.Errorf("failed to look up rename target: %w", err)
			case existing > math.MaxInt64-h.count:
				return RenameOverflow, 0, nil
			default:
				if _, err = tx.Exec(
					`UPDATE score_items SET count = count + ?, updated_at = CURRENT_TIMESTAMP
					 WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
					h.count, guildID, h.userID, toKey,
				); err == nil {
					_, err = tx.Exec(
						`DELETE FROM score_items WHERE guild_id = ? AND user_id = ? AND name_key = ?`,
						guildID, h.userID, fromKey,
					)
				}
			}
			if err != nil {
				return 0, 0, fmt.Errorf("failed to rename score item: %w", err)
			}
		}
	}

	if _, err := tx.Exec(
		`UPDATE score_items SET name = ?, updated_at = CURRENT_TIMESTAMP WHERE guild_id = ? AND name_key = ?`,
		to, guildID, toKey,
	); err != nil {
		return 0, 0, fmt.Errorf("failed to respell score item: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit rename: %w", err)
	}
	return RenameApplied, len(held), nil
}

// WipeScoreItem removes a thing from everyone in a guild, as if it was never
// given. people is how many users held it.
func (db *DB) WipeScoreItem(guildID, thing string) (people int, err error) {
	if thing, err = validateScoreItem(thing, 1); err != nil {
		return 0, err
	}
	res, err := db.conn.Exec(
		`DELETE FROM score_items WHERE guild_id = ? AND name_key = ?`,
		guildID, scoreItemKey(thing),
	)
	if err != nil {
		return 0, fmt.Errorf("failed to wipe score item: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to count wiped score items: %w", err)
	}
	return int(n), nil
}
