package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Slide puzzle persistence. Each guild has at most one community puzzle; the
// board, round deadline and running totals live in slide_puzzles so a restart
// picks up exactly where it left off. Votes are keyed by (guild, round, user),
// which is what enforces one vote per member per move.

const slidePuzzleSchema = `
CREATE TABLE IF NOT EXISTS slide_puzzles (
	guild_id    TEXT PRIMARY KEY,
	channel_id  TEXT NOT NULL,
	message_id  TEXT NOT NULL DEFAULT '',
	image_id    INTEGER NOT NULL DEFAULT 0,
	grid        INTEGER NOT NULL,
	board       TEXT NOT NULL,
	round       INTEGER NOT NULL DEFAULT 1,
	round_ends  INTEGER NOT NULL,
	total_votes INTEGER NOT NULL DEFAULT 0,
	total_moves INTEGER NOT NULL DEFAULT 0,
	solved      INTEGER NOT NULL DEFAULT 0,
	needs_redraw INTEGER NOT NULL DEFAULT 0,
	updated_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS slide_puzzle_votes (
	guild_id TEXT NOT NULL,
	round    INTEGER NOT NULL,
	user_id  TEXT NOT NULL,
	tile     INTEGER NOT NULL,
	voted_at INTEGER NOT NULL,
	PRIMARY KEY (guild_id, round, user_id)
);

CREATE TABLE IF NOT EXISTS slide_puzzle_images (
	id       INTEGER PRIMARY KEY AUTOINCREMENT,
	guild_id TEXT NOT NULL,
	name     TEXT NOT NULL,
	data     BLOB NOT NULL,
	added_by TEXT NOT NULL,
	added_at INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_slide_puzzle_images_guild ON slide_puzzle_images(guild_id);
`

// ErrSlidePuzzleStale is returned when a vote or move targets a round that is
// no longer current (or a puzzle that was solved or reset meanwhile).
var ErrSlidePuzzleStale = errors.New("slide puzzle round is no longer current")

// ErrSlidePuzzleRoundClosed is returned for a vote cast after the round's
// deadline but before the scheduler has made the move.
var ErrSlidePuzzleRoundClosed = errors.New("slide puzzle round has closed")

// SlidePuzzle is one guild's puzzle state. Board[pos] is the tile number at
// that position (tiles are numbered 1..n-1 in solved order); 0 is the gap.
type SlidePuzzle struct {
	GuildID    string
	ChannelID  string
	MessageID  string
	ImageID    int64 // 0 is the built-in default image
	Grid       int
	Board      []int
	Round      int64
	RoundEnds  time.Time
	TotalVotes int64
	TotalMoves int64
	Solved     bool
	// NeedsRedraw is set when the board changed but the new image has not
	// been published to Discord yet.
	NeedsRedraw bool
}

func encodeBoard(b []int) string {
	parts := make([]string, len(b))
	for i, v := range b {
		parts[i] = strconv.Itoa(v)
	}
	return strings.Join(parts, ",")
}

func decodeBoard(s string) ([]int, error) {
	parts := strings.Split(s, ",")
	out := make([]int, len(parts))
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil, fmt.Errorf("bad board cell %q: %w", p, err)
		}
		out[i] = n
	}
	return out, nil
}

const slidePuzzleColumns = `guild_id, channel_id, message_id, image_id, grid, board, round, round_ends, total_votes, total_moves, solved, needs_redraw`

func scanSlidePuzzle(row interface{ Scan(...any) error }) (SlidePuzzle, error) {
	var p SlidePuzzle
	var board string
	var ends int64
	var solved, redraw int
	if err := row.Scan(&p.GuildID, &p.ChannelID, &p.MessageID, &p.ImageID, &p.Grid, &board,
		&p.Round, &ends, &p.TotalVotes, &p.TotalMoves, &solved, &redraw); err != nil {
		return SlidePuzzle{}, err
	}
	b, err := decodeBoard(board)
	if err != nil {
		return SlidePuzzle{}, err
	}
	p.Board = b
	p.RoundEnds = time.Unix(ends, 0)
	p.Solved = solved != 0
	p.NeedsRedraw = redraw != 0
	return p, nil
}

// GetSlidePuzzle returns a guild's puzzle, if one has been set up.
func (db *DB) GetSlidePuzzle(guildID string) (SlidePuzzle, bool, error) {
	p, err := scanSlidePuzzle(db.conn.QueryRow(
		`SELECT `+slidePuzzleColumns+` FROM slide_puzzles WHERE guild_id = ?`, guildID))
	if errors.Is(err, sql.ErrNoRows) {
		return SlidePuzzle{}, false, nil
	}
	if err != nil {
		return SlidePuzzle{}, false, fmt.Errorf("failed to load slide puzzle: %w", err)
	}
	return p, true, nil
}

// StartSlidePuzzle (re)starts a guild's puzzle with a fresh, already-posted
// board: totals go back to zero, the round advances past any earlier round (so
// stale vote menus are rejected) and old votes are dropped.
func (db *DB) StartSlidePuzzle(p SlidePuzzle, now time.Time) (SlidePuzzle, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return SlidePuzzle{}, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var prevRound int64
	err = tx.QueryRow(`SELECT round FROM slide_puzzles WHERE guild_id = ?`, p.GuildID).Scan(&prevRound)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return SlidePuzzle{}, fmt.Errorf("failed to read previous round: %w", err)
	}
	p.Round = prevRound + 1
	p.TotalVotes, p.TotalMoves, p.Solved = 0, 0, false

	if _, err := tx.Exec(`
		INSERT INTO slide_puzzles (guild_id, channel_id, message_id, image_id, grid, board, round, round_ends, total_votes, total_moves, solved, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0, 0, 0, ?)
		ON CONFLICT(guild_id) DO UPDATE SET
			channel_id = excluded.channel_id, message_id = excluded.message_id, image_id = excluded.image_id,
			grid = excluded.grid, board = excluded.board, round = excluded.round, round_ends = excluded.round_ends,
			total_votes = 0, total_moves = 0, solved = 0, needs_redraw = 0, updated_at = excluded.updated_at`,
		p.GuildID, p.ChannelID, p.MessageID, p.ImageID, p.Grid, encodeBoard(p.Board), p.Round, p.RoundEnds.Unix(), now.Unix(),
	); err != nil {
		return SlidePuzzle{}, fmt.Errorf("failed to save slide puzzle: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM slide_puzzle_votes WHERE guild_id = ?`, p.GuildID); err != nil {
		return SlidePuzzle{}, fmt.Errorf("failed to clear slide puzzle votes: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return SlidePuzzle{}, fmt.Errorf("failed to commit slide puzzle: %w", err)
	}
	return p, nil
}

// MarkSlidePuzzlePublished clears the pending-redraw flag once the board for
// round has been published. A later move (higher round) keeps its own flag.
func (db *DB) MarkSlidePuzzlePublished(guildID string, round int64) error {
	_, err := db.conn.Exec(`UPDATE slide_puzzles SET needs_redraw = 0 WHERE guild_id = ? AND round = ?`, guildID, round)
	if err != nil {
		return fmt.Errorf("failed to mark slide puzzle published: %w", err)
	}
	return nil
}

// CastSlidePuzzleVote records a member's vote for the given round. It returns
// the existing tile and false when the member already voted this round,
// ErrSlidePuzzleStale when the round has moved on or the puzzle is solved, and
// ErrSlidePuzzleRoundClosed once the round's deadline has passed.
func (db *DB) CastSlidePuzzleVote(guildID string, round int64, userID string, tile int, now time.Time) (int, bool, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, false, fmt.Errorf("failed to begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	var cur, ends int64
	var solved int
	err = tx.QueryRow(`SELECT round, round_ends, solved FROM slide_puzzles WHERE guild_id = ?`, guildID).Scan(&cur, &ends, &solved)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, ErrSlidePuzzleStale
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to read slide puzzle: %w", err)
	}
	if cur != round || solved != 0 {
		return 0, false, ErrSlidePuzzleStale
	}
	if now.Unix() >= ends {
		return 0, false, ErrSlidePuzzleRoundClosed
	}

	var existing int
	err = tx.QueryRow(`SELECT tile FROM slide_puzzle_votes WHERE guild_id = ? AND round = ? AND user_id = ?`,
		guildID, round, userID).Scan(&existing)
	if err == nil {
		return existing, false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, false, fmt.Errorf("failed to check vote: %w", err)
	}

	if _, err := tx.Exec(`INSERT INTO slide_puzzle_votes (guild_id, round, user_id, tile, voted_at) VALUES (?, ?, ?, ?, ?)`,
		guildID, round, userID, tile, now.Unix()); err != nil {
		return 0, false, fmt.Errorf("failed to record vote: %w", err)
	}
	if _, err := tx.Exec(`UPDATE slide_puzzles SET total_votes = total_votes + 1 WHERE guild_id = ?`, guildID); err != nil {
		return 0, false, fmt.Errorf("failed to bump vote total: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, false, fmt.Errorf("failed to commit vote: %w", err)
	}
	return tile, true, nil
}

// SlidePuzzleTally returns vote counts per tile for a round.
func (db *DB) SlidePuzzleTally(guildID string, round int64) (map[int]int, error) {
	rows, err := db.conn.Query(`SELECT tile, COUNT(*) FROM slide_puzzle_votes WHERE guild_id = ? AND round = ? GROUP BY tile`,
		guildID, round)
	if err != nil {
		return nil, fmt.Errorf("failed to tally votes: %w", err)
	}
	defer rows.Close()
	out := map[int]int{}
	for rows.Next() {
		var tile, n int
		if err := rows.Scan(&tile, &n); err != nil {
			return nil, fmt.Errorf("failed to scan tally: %w", err)
		}
		out[tile] = n
	}
	return out, rows.Err()
}

// AdvanceSlidePuzzle applies an executed move: it stores the new board, bumps
// the move total, opens the next round, drops the finished round's votes and
// flags the board for redraw until MarkSlidePuzzlePublished.
// It returns ErrSlidePuzzleStale if the puzzle moved past round meanwhile.
func (db *DB) AdvanceSlidePuzzle(guildID string, round int64, board []int, solved bool, roundEnds, now time.Time) error {
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	res, err := tx.Exec(`
		UPDATE slide_puzzles SET board = ?, round = round + 1, round_ends = ?, total_moves = total_moves + 1,
			solved = ?, needs_redraw = 1, updated_at = ?
		WHERE guild_id = ? AND round = ? AND solved = 0`,
		encodeBoard(board), roundEnds.Unix(), boolToInt(solved), now.Unix(), guildID, round)
	if err != nil {
		return fmt.Errorf("failed to advance slide puzzle: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrSlidePuzzleStale
	}
	if _, err := tx.Exec(`DELETE FROM slide_puzzle_votes WHERE guild_id = ? AND round <= ?`, guildID, round); err != nil {
		return fmt.Errorf("failed to clear votes: %w", err)
	}
	return tx.Commit()
}

// ExtendSlidePuzzleRound pushes back the deadline of a round that got no votes.
func (db *DB) ExtendSlidePuzzleRound(guildID string, round int64, roundEnds time.Time) error {
	_, err := db.conn.Exec(`UPDATE slide_puzzles SET round_ends = ? WHERE guild_id = ? AND round = ?`,
		roundEnds.Unix(), guildID, round)
	if err != nil {
		return fmt.Errorf("failed to extend slide puzzle round: %w", err)
	}
	return nil
}

// DueSlidePuzzles returns posted puzzles that need scheduler attention: an
// unsolved round has ended, or a board update has not been published yet.
func (db *DB) DueSlidePuzzles(now time.Time) ([]SlidePuzzle, error) {
	rows, err := db.conn.Query(`SELECT `+slidePuzzleColumns+` FROM slide_puzzles
		WHERE message_id != '' AND (needs_redraw = 1 OR (solved = 0 AND round_ends <= ?))`, now.Unix())
	if err != nil {
		return nil, fmt.Errorf("failed to list due slide puzzles: %w", err)
	}
	defer rows.Close()
	var out []SlidePuzzle
	for rows.Next() {
		p, err := scanSlidePuzzle(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan slide puzzle: %w", err)
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// AddSlidePuzzleImage stores an uploaded image in a guild's pool.
func (db *DB) AddSlidePuzzleImage(guildID, name string, data []byte, addedBy string, now time.Time) (int64, error) {
	res, err := db.conn.Exec(`INSERT INTO slide_puzzle_images (guild_id, name, data, added_by, added_at) VALUES (?, ?, ?, ?, ?)`,
		guildID, name, data, addedBy, now.Unix())
	if err != nil {
		return 0, fmt.Errorf("failed to add slide puzzle image: %w", err)
	}
	return res.LastInsertId()
}

// SlidePuzzleImageIDs lists the uploaded image IDs in a guild's pool.
func (db *DB) SlidePuzzleImageIDs(guildID string) ([]int64, error) {
	rows, err := db.conn.Query(`SELECT id FROM slide_puzzle_images WHERE guild_id = ? ORDER BY id`, guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to list slide puzzle images: %w", err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// GetSlidePuzzleImage returns an uploaded image's bytes.
func (db *DB) GetSlidePuzzleImage(guildID string, id int64) ([]byte, bool, error) {
	var data []byte
	err := db.conn.QueryRow(`SELECT data FROM slide_puzzle_images WHERE guild_id = ? AND id = ?`, guildID, id).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("failed to load slide puzzle image: %w", err)
	}
	return data, true, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// SlidePuzzleUserVote returns the tile a member voted for in a round, if any.
func (db *DB) SlidePuzzleUserVote(guildID string, round int64, userID string) (int, bool, error) {
	var tile int
	err := db.conn.QueryRow(`SELECT tile FROM slide_puzzle_votes WHERE guild_id = ? AND round = ? AND user_id = ?`,
		guildID, round, userID).Scan(&tile)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("failed to read vote: %w", err)
	}
	return tile, true, nil
}
