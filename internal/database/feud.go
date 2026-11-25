package database

import (
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"time"
)

// GP Feud persistence. Questions are seeded or submitted by members (and
// approved by mods), then collect answers until enough people have responded
// to be played. Each response belongs to exactly one answer group; matching
// responses share a group, and the group's score is how many people gave it.

const feudSchema = `
CREATE TABLE IF NOT EXISTS feud_guilds (
	guild_id     TEXT PRIMARY KEY,
	seeded_at    INTEGER NOT NULL DEFAULT 0,
	games_played INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS feud_questions (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	guild_id          TEXT NOT NULL,
	text              TEXT NOT NULL,
	norm              TEXT NOT NULL,
	status            TEXT NOT NULL,
	source            TEXT NOT NULL,
	submitted_by      TEXT NOT NULL DEFAULT '',
	reviewed_by       TEXT NOT NULL DEFAULT '',
	review_channel_id TEXT NOT NULL DEFAULT '',
	review_message_id TEXT NOT NULL DEFAULT '',
	created_at        INTEGER NOT NULL,
	reviewed_at       INTEGER NOT NULL DEFAULT 0,
	last_played_game  INTEGER NOT NULL DEFAULT 0,
	times_played      INTEGER NOT NULL DEFAULT 0,
	UNIQUE (guild_id, norm)
);
CREATE INDEX IF NOT EXISTS idx_feud_questions_status ON feud_questions(guild_id, status);

CREATE TABLE IF NOT EXISTS feud_answers (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	question_id INTEGER NOT NULL,
	label       TEXT NOT NULL,
	norm        TEXT NOT NULL,
	extra       INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_feud_answers_question ON feud_answers(question_id);

CREATE TABLE IF NOT EXISTS feud_responses (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	question_id INTEGER NOT NULL,
	user_id     TEXT NOT NULL,
	text        TEXT NOT NULL,
	norm        TEXT NOT NULL,
	answer_id   INTEGER NOT NULL,
	rejected    INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL,
	UNIQUE (question_id, user_id)
);
CREATE INDEX IF NOT EXISTS idx_feud_responses_answer ON feud_responses(answer_id);
`

// Question statuses.
const (
	FeudPending  = "pending"
	FeudActive   = "active"
	FeudRejected = "rejected"
)

// Question sources.
const (
	FeudSourceSeed = "seed"
	FeudSourceUser = "user"
)

var (
	// ErrFeudDuplicateQuestion is returned when a guild already has the question.
	ErrFeudDuplicateQuestion = errors.New("feud question already exists")
	// ErrFeudTooManyPending is returned when a member has too many questions awaiting review.
	ErrFeudTooManyPending = errors.New("too many pending feud questions")
	// ErrFeudAlreadyAnswered is returned for a second answer from the same member.
	ErrFeudAlreadyAnswered = errors.New("already answered this feud question")
	// ErrFeudNotActive is returned when a question isn't collecting answers.
	ErrFeudNotActive = errors.New("feud question is not active")
	// ErrFeudNotPending is returned when a question was already reviewed.
	ErrFeudNotPending = errors.New("feud question was already reviewed")
)

// FeudQuestion is one survey question.
type FeudQuestion struct {
	ID              int64
	GuildID         string
	Text            string
	Status          string
	Source          string
	SubmittedBy     string
	ReviewedBy      string
	ReviewChannelID string
	ReviewMessageID string
	CreatedAt       time.Time
	ReviewedAt      time.Time
	LastPlayedGame  int64
	TimesPlayed     int
	// Respondents counts members whose answer wasn't rejected. It is only
	// filled in by queries that say so.
	Respondents int
}

// FeudAnswer is one answer group: every response that means the same thing.
type FeudAnswer struct {
	ID         int64
	QuestionID int64
	Label      string
	Norm       string
	Count      int // responses that weren't rejected
	Extra      int // points a mod added by hand
}

// Score is the answer's value on the board.
func (a FeudAnswer) Score() int { return a.Count + a.Extra }

const feudQuestionCols = `id, guild_id, text, status, source, submitted_by, reviewed_by,
	review_channel_id, review_message_id, created_at, reviewed_at, last_played_game, times_played`

func scanFeudQuestion(row interface{ Scan(...any) error }, extra ...any) (FeudQuestion, error) {
	var q FeudQuestion
	var created, reviewed int64
	dest := append([]any{&q.ID, &q.GuildID, &q.Text, &q.Status, &q.Source, &q.SubmittedBy, &q.ReviewedBy,
		&q.ReviewChannelID, &q.ReviewMessageID, &created, &reviewed, &q.LastPlayedGame, &q.TimesPlayed}, extra...)
	if err := row.Scan(dest...); err != nil {
		return FeudQuestion{}, err
	}
	q.CreatedAt = time.Unix(created, 0)
	if reviewed > 0 {
		q.ReviewedAt = time.Unix(reviewed, 0)
	}
	return q, nil
}

// SeedFeudQuestions adds the starter questions the first time a guild uses GP
// Feud. Later calls do nothing, so mods can retire seeds for good. Questions
// the guild already has are skipped. It reports how many were added.
func (db *DB) SeedFeudQuestions(guildID string, questions []FeudSeed, now time.Time) (int, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var seeded int64
	err = tx.QueryRow(`SELECT seeded_at FROM feud_guilds WHERE guild_id = ?`, guildID).Scan(&seeded)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	if seeded > 0 {
		return 0, nil
	}
	added := 0
	for _, q := range questions {
		res, err := tx.Exec(`INSERT INTO feud_questions (guild_id, text, norm, status, source, created_at)
			VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT (guild_id, norm) DO NOTHING`,
			guildID, q.Text, q.Norm, FeudActive, FeudSourceSeed, now.Unix())
		if err != nil {
			return 0, fmt.Errorf("seed feud question: %w", err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if _, err := tx.Exec(`INSERT INTO feud_guilds (guild_id, seeded_at) VALUES (?, ?)
		ON CONFLICT (guild_id) DO UPDATE SET seeded_at = excluded.seeded_at`, guildID, now.Unix()); err != nil {
		return 0, err
	}
	return added, tx.Commit()
}

// FeudSeed is a starter question with its normalized form.
type FeudSeed struct {
	Text string
	Norm string
}

// SubmitFeudQuestion stores a member's question for mod review. The pending
// limit is enforced in the same statement as the insert, so concurrent
// submissions cannot exceed it.
func (db *DB) SubmitFeudQuestion(guildID, userID, text, norm string, maxPending int, now time.Time) (int64, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, fmt.Errorf("submit feud question: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	res, err := tx.Exec(`INSERT INTO feud_questions (guild_id, text, norm, status, source, submitted_by, created_at)
		SELECT ?, ?, ?, ?, ?, ?, ?
		WHERE (SELECT COUNT(*) FROM feud_questions WHERE guild_id = ? AND submitted_by = ? AND status = ?) < ?
		ON CONFLICT (guild_id, norm) DO NOTHING`,
		guildID, text, norm, FeudPending, FeudSourceUser, userID, now.Unix(),
		guildID, userID, FeudPending, maxPending)
	if err != nil {
		return 0, fmt.Errorf("submit feud question: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var dup int
		if err := tx.QueryRow(`SELECT COUNT(*) FROM feud_questions WHERE guild_id = ? AND norm = ?`, guildID, norm).Scan(&dup); err != nil {
			return 0, fmt.Errorf("submit feud question: %w", err)
		}
		if dup > 0 {
			return 0, ErrFeudDuplicateQuestion
		}
		return 0, ErrFeudTooManyPending
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// CountFeudPendingBy returns how many of a member's questions await review.
func (db *DB) CountFeudPendingBy(guildID, userID string) (int, error) {
	var n int
	err := db.conn.QueryRow(`SELECT COUNT(*) FROM feud_questions WHERE guild_id = ? AND submitted_by = ? AND status = ?`,
		guildID, userID, FeudPending).Scan(&n)
	return n, err
}

// SetFeudQuestionReviewMessage remembers the review post for a question.
func (db *DB) SetFeudQuestionReviewMessage(id int64, channelID, messageID string) error {
	_, err := db.conn.Exec(`UPDATE feud_questions SET review_channel_id = ?, review_message_id = ? WHERE id = ?`,
		channelID, messageID, id)
	return err
}

// ReviewFeudQuestion approves or rejects a pending question. It returns
// ErrFeudNotPending if someone already reviewed it.
func (db *DB) ReviewFeudQuestion(guildID string, id int64, approve bool, modID string, now time.Time) (FeudQuestion, error) {
	status := FeudRejected
	if approve {
		status = FeudActive
	}
	res, err := db.conn.Exec(`UPDATE feud_questions SET status = ?, reviewed_by = ?, reviewed_at = ?
		WHERE id = ? AND guild_id = ? AND status = ?`, status, modID, now.Unix(), id, guildID, FeudPending)
	if err != nil {
		return FeudQuestion{}, err
	}
	q, ok, err := db.FeudQuestion(guildID, id)
	if err != nil {
		return FeudQuestion{}, err
	}
	if !ok {
		return FeudQuestion{}, sql.ErrNoRows
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return q, ErrFeudNotPending
	}
	return q, nil
}

// FeudQuestion loads one question with its respondent count.
func (db *DB) FeudQuestion(guildID string, id int64) (FeudQuestion, bool, error) {
	row := db.conn.QueryRow(`SELECT `+feudQuestionCols+`,
		(SELECT COUNT(*) FROM feud_responses r WHERE r.question_id = q.id AND r.rejected = 0)
		FROM feud_questions q WHERE id = ? AND guild_id = ?`, id, guildID)
	var n int
	q, err := scanFeudQuestion(row, &n)
	if errors.Is(err, sql.ErrNoRows) {
		return FeudQuestion{}, false, nil
	}
	if err != nil {
		return FeudQuestion{}, false, err
	}
	q.Respondents = n
	return q, true, nil
}

// OldestPendingFeudQuestion returns the next question waiting for review.
func (db *DB) OldestPendingFeudQuestion(guildID string) (FeudQuestion, bool, error) {
	row := db.conn.QueryRow(`SELECT `+feudQuestionCols+` FROM feud_questions
		WHERE guild_id = ? AND status = ? ORDER BY id LIMIT 1`, guildID, FeudPending)
	q, err := scanFeudQuestion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return FeudQuestion{}, false, nil
	}
	if err != nil {
		return FeudQuestion{}, false, err
	}
	return q, true, nil
}

// FeudQuestionsToAnswer lists active questions the member hasn't answered,
// with their respondent counts.
func (db *DB) FeudQuestionsToAnswer(guildID, userID string) ([]FeudQuestion, error) {
	rows, err := db.conn.Query(`SELECT `+feudQuestionCols+`,
		(SELECT COUNT(*) FROM feud_responses r WHERE r.question_id = q.id AND r.rejected = 0)
		FROM feud_questions q
		WHERE guild_id = ? AND status = ?
		AND NOT EXISTS (SELECT 1 FROM feud_responses r WHERE r.question_id = q.id AND r.user_id = ?)
		ORDER BY id`, guildID, FeudActive, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeudQuestion
	for rows.Next() {
		var n int
		q, err := scanFeudQuestion(rows, &n)
		if err != nil {
			return nil, err
		}
		q.Respondents = n
		out = append(out, q)
	}
	return out, rows.Err()
}

// FeudPoolStats counts a guild's questions: awaiting review, still collecting
// answers, and ready to play (at least minRespondents answers).
type FeudPoolStats struct {
	Pending, Collecting, Ready int
}

// FeudPoolStats summarizes the question pool.
func (db *DB) FeudPoolStats(guildID string, minRespondents int) (FeudPoolStats, error) {
	var st FeudPoolStats
	err := db.conn.QueryRow(`SELECT
		COALESCE(SUM(status = ?), 0),
		COALESCE(SUM(status = ? AND n < ?), 0),
		COALESCE(SUM(status = ? AND n >= ?), 0)
		FROM (SELECT status, (SELECT COUNT(*) FROM feud_responses r WHERE r.question_id = q.id AND r.rejected = 0) AS n
			FROM feud_questions q WHERE guild_id = ?)`,
		FeudPending, FeudActive, minRespondents, FeudActive, minRespondents, guildID).
		Scan(&st.Pending, &st.Collecting, &st.Ready)
	return st, err
}

// AddFeudResponse records a member's answer. It joins the answer group that
// already has the same normalized text (from its label or another response),
// or starts a new group.
func (db *DB) AddFeudResponse(guildID string, questionID int64, userID, text, norm string, now time.Time) (FeudAnswer, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return FeudAnswer{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var status string
	err = tx.QueryRow(`SELECT status FROM feud_questions WHERE id = ? AND guild_id = ?`, questionID, guildID).Scan(&status)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && status != FeudActive) {
		return FeudAnswer{}, ErrFeudNotActive
	}
	if err != nil {
		return FeudAnswer{}, err
	}
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feud_responses WHERE question_id = ? AND user_id = ?`,
		questionID, userID).Scan(&exists); err != nil {
		return FeudAnswer{}, err
	}
	if exists > 0 {
		return FeudAnswer{}, ErrFeudAlreadyAnswered
	}
	var answerID int64
	err = tx.QueryRow(`SELECT id FROM feud_answers WHERE question_id = ? AND norm = ?
		UNION ALL SELECT answer_id FROM feud_responses WHERE question_id = ? AND norm = ? AND rejected = 0
		LIMIT 1`, questionID, norm, questionID, norm).Scan(&answerID)
	if errors.Is(err, sql.ErrNoRows) {
		res, err := tx.Exec(`INSERT INTO feud_answers (question_id, label, norm, created_at) VALUES (?, ?, ?, ?)`,
			questionID, text, norm, now.Unix())
		if err != nil {
			return FeudAnswer{}, err
		}
		if answerID, err = res.LastInsertId(); err != nil {
			return FeudAnswer{}, err
		}
	} else if err != nil {
		return FeudAnswer{}, err
	}
	if _, err := tx.Exec(`INSERT INTO feud_responses (question_id, user_id, text, norm, answer_id, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`, questionID, userID, text, norm, answerID, now.Unix()); err != nil {
		return FeudAnswer{}, err
	}
	a, err := feudAnswerTx(tx, answerID)
	if err != nil {
		return FeudAnswer{}, err
	}
	return a, tx.Commit()
}

type queryRower interface {
	QueryRow(query string, args ...any) *sql.Row
}

func feudAnswerTx(q queryRower, id int64) (FeudAnswer, error) {
	var a FeudAnswer
	err := q.QueryRow(`SELECT id, question_id, label, norm, extra,
		(SELECT COUNT(*) FROM feud_responses r WHERE r.answer_id = a.id AND r.rejected = 0)
		FROM feud_answers a WHERE id = ?`, id).Scan(&a.ID, &a.QuestionID, &a.Label, &a.Norm, &a.Extra, &a.Count)
	return a, err
}

// FeudAnswers lists a question's answer groups that score at least one point,
// highest score first (oldest first on ties).
func (db *DB) FeudAnswers(questionID int64) ([]FeudAnswer, error) {
	rows, err := db.conn.Query(`SELECT id, question_id, label, norm, extra,
		(SELECT COUNT(*) FROM feud_responses r WHERE r.answer_id = a.id AND r.rejected = 0)
		FROM feud_answers a WHERE question_id = ?`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FeudAnswer
	for rows.Next() {
		var a FeudAnswer
		if err := rows.Scan(&a.ID, &a.QuestionID, &a.Label, &a.Norm, &a.Extra, &a.Count); err != nil {
			return nil, err
		}
		if a.Score() > 0 {
			out = append(out, a)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score() != out[j].Score() {
			return out[i].Score() > out[j].Score()
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}
