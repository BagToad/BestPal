package database

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Mod and agent edits to GP Feud questions and answer groups. Raw responses
// are never deleted: merging moves them between groups and hiding keeps the
// group (and its responses) on record while keeping it off the board.

var (
	// ErrFeudAnswerExists is returned when another group of the question
	// already has the same normalized text.
	ErrFeudAnswerExists = errors.New("feud answer already exists")
	// ErrFeudAnswerNotFound is returned for an answer that isn't in the question.
	ErrFeudAnswerNotFound = errors.New("feud answer not found")
	// ErrFeudQuestionNotFound is returned for an unknown question.
	ErrFeudQuestionNotFound = errors.New("feud question not found")
	// ErrFeudBadStatus is returned when a status change isn't allowed.
	ErrFeudBadStatus = errors.New("feud question can't change to that status")
)

// migrateFeudAnswers adds columns missing from databases created before they
// existed.
func (db *DB) migrateFeudAnswers() error {
	rows, err := db.conn.Query(`PRAGMA table_info(feud_answers)`)
	if err != nil {
		return fmt.Errorf("failed to check feud_answers schema: %w", err)
	}
	have := map[string]bool{}
	for rows.Next() {
		var cid, notnull, pk int
		var name, ctype string
		var dflt *string
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("failed to scan feud_answers schema: %w", err)
		}
		have[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return fmt.Errorf("failed to read feud_answers schema: %w", err)
	}
	if !have["hidden"] {
		if _, err := db.conn.Exec(`ALTER TABLE feud_answers ADD COLUMN hidden INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("failed to add feud_answers.hidden: %w", err)
		}
	}
	return nil
}

// FeudQuestionsPage lists a guild's questions for the mod editor: active
// ones first, then pending, then retired, each oldest first.
func (db *DB) FeudQuestionsPage(guildID string, offset, limit int) ([]FeudQuestion, int, error) {
	var total int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM feud_questions WHERE guild_id = ?`, guildID).Scan(&total); err != nil {
		return nil, 0, err
	}
	rows, err := db.conn.Query(`SELECT `+feudQuestionCols+`,
		(SELECT COUNT(*) FROM feud_responses r WHERE r.question_id = q.id AND r.rejected = 0)
		FROM feud_questions q WHERE guild_id = ?
		ORDER BY CASE status WHEN ? THEN 0 WHEN ? THEN 1 ELSE 2 END, id
		LIMIT ? OFFSET ?`, guildID, FeudActive, FeudPending, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []FeudQuestion
	for rows.Next() {
		var n int
		q, err := scanFeudQuestion(rows, &n)
		if err != nil {
			return nil, 0, err
		}
		q.Respondents = n
		out = append(out, q)
	}
	return out, total, rows.Err()
}

// FeudActiveQuestionIDs lists a guild's active questions that have at least
// minAnswers answer groups, oldest first.
func (db *DB) FeudActiveQuestionIDs(guildID string, minAnswers int) ([]int64, error) {
	rows, err := db.conn.Query(`SELECT id FROM feud_questions q WHERE guild_id = ? AND status = ?
		AND (SELECT COUNT(*) FROM feud_answers a WHERE a.question_id = q.id AND a.hidden = 0) >= ?
		ORDER BY id`, guildID, FeudActive, minAnswers)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// UpdateFeudQuestionText rewrites a question's wording.
func (db *DB) UpdateFeudQuestionText(guildID string, id int64, text, norm string) error {
	var clash int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM feud_questions WHERE guild_id = ? AND norm = ? AND id != ?`,
		guildID, norm, id).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 {
		return ErrFeudDuplicateQuestion
	}
	res, err := db.conn.Exec(`UPDATE feud_questions SET text = ?, norm = ? WHERE id = ? AND guild_id = ?`, text, norm, id, guildID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeudQuestionNotFound
	}
	return nil
}

// SetFeudQuestionRetired retires an active question or restores a retired
// one. Pending questions go through review instead.
func (db *DB) SetFeudQuestionRetired(guildID string, id int64, retired bool) error {
	from, to := FeudRejected, FeudActive
	if retired {
		from, to = FeudActive, FeudRejected
	}
	res, err := db.conn.Exec(`UPDATE feud_questions SET status = ? WHERE id = ? AND guild_id = ? AND status = ?`, to, id, guildID, from)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeudBadStatus
	}
	return nil
}

// FeudAnswerIn loads an answer group, checking it belongs to the guild. It
// returns ErrFeudAnswerNotFound otherwise.
func (db *DB) FeudAnswerIn(guildID string, id int64) (FeudAnswer, error) {
	a, err := feudAnswerTx(db.conn, id)
	if errors.Is(err, sql.ErrNoRows) {
		return FeudAnswer{}, ErrFeudAnswerNotFound
	}
	if err != nil {
		return FeudAnswer{}, err
	}
	var n int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM feud_questions WHERE id = ? AND guild_id = ?`, a.QuestionID, guildID).Scan(&n); err != nil {
		return FeudAnswer{}, err
	}
	if n == 0 {
		return FeudAnswer{}, ErrFeudAnswerNotFound
	}
	return a, nil
}

// RenameFeudAnswer changes a group's label and the text new answers are
// matched against. Older responses keep matching through their own text.
func (db *DB) RenameFeudAnswer(questionID, id int64, label, norm string) error {
	var clash int
	if err := db.conn.QueryRow(`SELECT COUNT(*) FROM feud_answers WHERE question_id = ? AND norm = ? AND id != ?`,
		questionID, norm, id).Scan(&clash); err != nil {
		return err
	}
	if clash > 0 {
		return ErrFeudAnswerExists
	}
	res, err := db.conn.Exec(`UPDATE feud_answers SET label = ?, norm = ? WHERE id = ? AND question_id = ?`, label, norm, id, questionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeudAnswerNotFound
	}
	return nil
}

// SetFeudAnswerExtra sets the points a mod adds to (or, if negative, takes
// from) a group.
func (db *DB) SetFeudAnswerExtra(questionID, id int64, extra int) error {
	res, err := db.conn.Exec(`UPDATE feud_answers SET extra = ? WHERE id = ? AND question_id = ?`, extra, id, questionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeudAnswerNotFound
	}
	return nil
}

// SetFeudAnswerHidden keeps a group off the board, or puts it back.
func (db *DB) SetFeudAnswerHidden(questionID, id int64, hidden bool) error {
	res, err := db.conn.Exec(`UPDATE feud_answers SET hidden = ? WHERE id = ? AND question_id = ?`, hidden, id, questionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrFeudAnswerNotFound
	}
	return nil
}

// AddFeudAnswer creates a group by hand, worth the given points.
func (db *DB) AddFeudAnswer(questionID int64, label, norm string, points int, now time.Time) (int64, error) {
	tx, err := db.conn.Begin()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	var clash int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feud_answers WHERE question_id = ? AND norm = ?`, questionID, norm).Scan(&clash); err != nil {
		return 0, err
	}
	if clash > 0 {
		return 0, ErrFeudAnswerExists
	}
	res, err := tx.Exec(`INSERT INTO feud_answers (question_id, label, norm, extra, created_at) VALUES (?, ?, ?, ?, ?)`,
		questionID, label, norm, points, now.Unix())
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	return id, tx.Commit()
}

// MergeFeudAnswers folds one group into another: its responses and bonus
// points move over and the empty group is removed. The responses keep their
// own text, so what people typed still matches during play.
func (db *DB) MergeFeudAnswers(questionID, from, into int64) error {
	if from == into {
		return ErrFeudAnswerNotFound
	}
	tx, err := db.conn.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	var fromExtra, found int
	if err := tx.QueryRow(`SELECT extra FROM feud_answers WHERE id = ? AND question_id = ?`, from, questionID).Scan(&fromExtra); errors.Is(err, sql.ErrNoRows) {
		return ErrFeudAnswerNotFound
	} else if err != nil {
		return err
	}
	if err := tx.QueryRow(`SELECT COUNT(*) FROM feud_answers WHERE id = ? AND question_id = ?`, into, questionID).Scan(&found); err != nil {
		return err
	}
	if found == 0 {
		return ErrFeudAnswerNotFound
	}
	if _, err := tx.Exec(`UPDATE feud_responses SET answer_id = ? WHERE answer_id = ?`, into, from); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE feud_answers SET extra = extra + ? WHERE id = ?`, fromExtra, into); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM feud_answers WHERE id = ?`, from); err != nil {
		return err
	}
	return tx.Commit()
}

// FeudResponseTexts lists the distinct raw answers behind each of a
// question's groups, as people typed them.
func (db *DB) FeudResponseTexts(questionID int64) (map[int64][]string, error) {
	rows, err := db.conn.Query(`SELECT answer_id, text FROM feud_responses WHERE question_id = ? AND rejected = 0
		GROUP BY answer_id, text ORDER BY answer_id, MIN(id)`, questionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64][]string{}
	for rows.Next() {
		var id int64
		var text string
		if err := rows.Scan(&id, &text); err != nil {
			return nil, err
		}
		out[id] = append(out[id], text)
	}
	return out, rows.Err()
}
