package database

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// GP Feud games. The live game is one JSON document owned by the feud module,
// which changes it under a per-guild lock; at most one game per guild is
// unfinished at a time.

const feudGameSchema = `
CREATE TABLE IF NOT EXISTS feud_boards (
	guild_id       TEXT PRIMARY KEY,
	channel_id     TEXT NOT NULL,
	message_id     TEXT NOT NULL,
	red_thread_id  TEXT NOT NULL,
	blue_thread_id TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS feud_games (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	guild_id   TEXT NOT NULL,
	ended      INTEGER NOT NULL DEFAULT 0,
	state      TEXT NOT NULL,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_feud_games_live ON feud_games(guild_id) WHERE ended = 0;
`

// ErrFeudGameRunning is returned when a guild already has an unfinished game.
var ErrFeudGameRunning = errors.New("a feud game is already running")

// FeudBoard is where a guild plays: the board message and the team threads.
type FeudBoard struct {
	GuildID      string
	ChannelID    string
	MessageID    string
	RedThreadID  string
	BlueThreadID string
}

// FeudBoard loads a guild's board.
func (db *DB) FeudBoard(guildID string) (FeudBoard, bool, error) {
	b := FeudBoard{GuildID: guildID}
	err := db.conn.QueryRow(`SELECT channel_id, message_id, red_thread_id, blue_thread_id FROM feud_boards WHERE guild_id = ?`,
		guildID).Scan(&b.ChannelID, &b.MessageID, &b.RedThreadID, &b.BlueThreadID)
	if errors.Is(err, sql.ErrNoRows) {
		return FeudBoard{}, false, nil
	}
	return b, err == nil, err
}

// SaveFeudBoard stores a guild's board.
func (db *DB) SaveFeudBoard(b FeudBoard) error {
	_, err := db.conn.Exec(`INSERT INTO feud_boards (guild_id, channel_id, message_id, red_thread_id, blue_thread_id)
		VALUES (?, ?, ?, ?, ?) ON CONFLICT (guild_id) DO UPDATE SET channel_id = excluded.channel_id,
		message_id = excluded.message_id, red_thread_id = excluded.red_thread_id, blue_thread_id = excluded.blue_thread_id`,
		b.GuildID, b.ChannelID, b.MessageID, b.RedThreadID, b.BlueThreadID)
	return err
}

// FeudGameRow is a stored game.
type FeudGameRow struct {
	ID      int64
	GuildID string
	Ended   bool
	State   string
}

// CreateFeudGame starts a game and counts it toward the question cooldown.
// It returns the game ID and the guild's game number.
func (db *DB) CreateFeudGame(guildID, state string, now time.Time) (int64, int64, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, 0, err
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO feud_games (guild_id, state, created_at, updated_at) VALUES (?, ?, ?, ?)`,
		guildID, state, now.Unix(), now.Unix())
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return 0, 0, ErrFeudGameRunning
		}
		return 0, 0, fmt.Errorf("create feud game: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, 0, err
	}
	var n int64
	if err := tx.QueryRow(`INSERT INTO feud_guilds (guild_id, games_played) VALUES (?, 1)
		ON CONFLICT (guild_id) DO UPDATE SET games_played = games_played + 1
		RETURNING games_played`, guildID).Scan(&n); err != nil {
		return 0, 0, err
	}
	return id, n, tx.Commit()
}

// SaveFeudGame stores a game's state.
func (db *DB) SaveFeudGame(id int64, state string, ended bool, now time.Time) error {
	_, err := db.conn.Exec(`UPDATE feud_games SET state = ?, ended = ?, updated_at = ? WHERE id = ?`,
		state, ended, now.Unix(), id)
	return err
}

// LiveFeudGame returns a guild's unfinished game.
func (db *DB) LiveFeudGame(guildID string) (FeudGameRow, bool, error) {
	g := FeudGameRow{GuildID: guildID}
	err := db.conn.QueryRow(`SELECT id, state FROM feud_games WHERE guild_id = ? AND ended = 0`, guildID).Scan(&g.ID, &g.State)
	if errors.Is(err, sql.ErrNoRows) {
		return FeudGameRow{}, false, nil
	}
	return g, err == nil, err
}

// LiveFeudGames lists every unfinished game, for restarts.
func (db *DB) LiveFeudGames() ([]FeudGameRow, error) {
	rows, err := db.conn.Query(`SELECT id, guild_id, state FROM feud_games WHERE ended = 0 ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeudGameRow
	for rows.Next() {
		var g FeudGameRow
		if err := rows.Scan(&g.ID, &g.GuildID, &g.State); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// PickFeudQuestion chooses a playable question (at least minRespondents
// answers) that hasn't been played in the last cooldown games, skipping the
// excluded ones. When every playable question is cooling down, the one played
// longest ago is used. pick chooses among the fresh ones by index.
func (db *DB) PickFeudQuestion(guildID string, minRespondents int, cooldown, gameNo int64, exclude []int64, pick func(n int) int) (FeudQuestion, bool, error) {
	rows, err := db.conn.Query(`SELECT `+feudQuestionCols+`, n FROM (
		SELECT q.*, `+feudRespondents+` AS n
		FROM feud_questions q WHERE guild_id = ? AND status = ?) q
		WHERE n >= ? AND `+feudHasBoard+` ORDER BY last_played_game, id`, guildID, FeudActive, minRespondents)
	if err != nil {
		return FeudQuestion{}, false, err
	}
	defer rows.Close()
	skip := map[int64]bool{}
	for _, id := range exclude {
		skip[id] = true
	}
	var fresh, all []FeudQuestion
	for rows.Next() {
		var n int
		q, err := scanFeudQuestion(rows, &n)
		if err != nil {
			return FeudQuestion{}, false, err
		}
		q.Respondents = n
		if skip[q.ID] {
			continue
		}
		all = append(all, q)
		if q.LastPlayedGame == 0 || gameNo-q.LastPlayedGame > cooldown {
			fresh = append(fresh, q)
		}
	}
	if err := rows.Err(); err != nil {
		return FeudQuestion{}, false, err
	}
	switch {
	case len(fresh) > 0:
		return fresh[pick(len(fresh))], true, nil
	case len(all) > 0:
		return all[0], true, nil
	}
	return FeudQuestion{}, false, nil
}

// MarkFeudQuestionPlayed records that a game used a question.
func (db *DB) MarkFeudQuestionPlayed(id, gameNo int64) error {
	_, err := db.conn.Exec(`UPDATE feud_questions SET last_played_game = ?, times_played = times_played + 1 WHERE id = ?`, gameNo, id)
	return err
}

// FeudAnswerAliases maps each answer to the normalized texts that count as it:
// its own and those of every accepted response in the group.
func (db *DB) FeudAnswerAliases(questionID int64) (map[int64][]string, error) {
	rows, err := db.conn.Query(`SELECT id, norm FROM feud_answers WHERE question_id = ?
		UNION SELECT answer_id, norm FROM feud_answer_aliases WHERE question_id = ?
		UNION SELECT answer_id, norm FROM feud_responses WHERE question_id = ? AND rejected = 0`, questionID, questionID, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var norm string
		if err := rows.Scan(&id, &norm); err != nil {
			return nil, err
		}
		out[id] = append(out[id], norm)
	}
	return out, rows.Err()
}

// LastFeudGame returns a guild's most recent game, finished or not.
func (db *DB) LastFeudGame(guildID string) (FeudGameRow, bool, error) {
	g := FeudGameRow{GuildID: guildID}
	err := db.conn.QueryRow(`SELECT id, ended, state FROM feud_games WHERE guild_id = ? ORDER BY id DESC LIMIT 1`, guildID).
		Scan(&g.ID, &g.Ended, &g.State)
	if errors.Is(err, sql.ErrNoRows) {
		return FeudGameRow{}, false, nil
	}
	return g, err == nil, err
}
