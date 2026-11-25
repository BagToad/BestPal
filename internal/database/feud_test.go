package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func feudQuestionID(t *testing.T, db *DB, guildID, norm string) int64 {
	t.Helper()
	var id int64
	require.NoError(t, db.conn.QueryRow(`SELECT id FROM feud_questions WHERE guild_id = ? AND norm = ?`, guildID, norm).Scan(&id))
	return id
}

func TestFeudSeedOnce(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	seeds := []FeudSeed{{"Name a fruit.", "name fruit"}, {"Name a fruit!", "name fruit"}, {"Name a pet.", "name pet"}}
	n, err := db.SeedFeudQuestions("g", seeds, now)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "duplicate seeds are skipped")

	// Retiring a seed doesn't bring it back on the next call.
	_, err = db.conn.Exec(`DELETE FROM feud_questions WHERE norm = 'name pet'`)
	require.NoError(t, err)
	n, err = db.SeedFeudQuestions("g", seeds, now)
	require.NoError(t, err)
	assert.Zero(t, n)

	n, err = db.SeedFeudQuestions("other", seeds, now)
	require.NoError(t, err)
	assert.Equal(t, 2, n, "each guild gets its own seeds")
}

func TestFeudSubmitAndReview(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	id, err := db.SubmitFeudQuestion("g", "u", "Name a snack.", "name snack", 3, now)
	require.NoError(t, err)
	_, err = db.SubmitFeudQuestion("g", "u2", "name a SNACK", "name snack", 3, now)
	assert.ErrorIs(t, err, ErrFeudDuplicateQuestion)
	n, err := db.CountFeudPendingBy("g", "u")
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	q, ok, err := db.OldestPendingFeudQuestion("g")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, id, q.ID)

	_, err = db.AddFeudResponse("g", id, "a", "Chips", "chip", now)
	assert.ErrorIs(t, err, ErrFeudNotActive, "pending questions don't take answers")

	_, err = db.ReviewFeudQuestion("other", id, true, "mod", now)
	assert.Error(t, err, "another guild can't review it")
	q, err = db.ReviewFeudQuestion("g", id, true, "mod", now)
	require.NoError(t, err)
	assert.Equal(t, FeudActive, q.Status)
	assert.Equal(t, "mod", q.ReviewedBy)
	q, err = db.ReviewFeudQuestion("g", id, false, "mod2", now)
	assert.ErrorIs(t, err, ErrFeudNotPending)
	assert.Equal(t, FeudActive, q.Status, "the first decision stands")

	_, ok, err = db.OldestPendingFeudQuestion("g")
	require.NoError(t, err)
	assert.False(t, ok)
}

func TestFeudResponsesGroupByNorm(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	_, err := db.SeedFeudQuestions("g", []FeudSeed{{"Name a pet.", "name pet"}}, now)
	require.NoError(t, err)
	qid := feudQuestionID(t, db, "g", "name pet")

	a, err := db.AddFeudResponse("g", qid, "u1", "Dog", "dog", now)
	require.NoError(t, err)
	assert.Equal(t, 1, a.Count)
	b, err := db.AddFeudResponse("g", qid, "u2", "dogs", "dog", now)
	require.NoError(t, err)
	assert.Equal(t, a.ID, b.ID)
	assert.Equal(t, 2, b.Count)
	assert.Equal(t, "Dog", b.Label, "the first wording labels the group")
	_, err = db.AddFeudResponse("g", qid, "u3", "Cat", "cat", now)
	require.NoError(t, err)
	_, err = db.AddFeudResponse("g", qid, "u1", "Fish", "fish", now)
	assert.ErrorIs(t, err, ErrFeudAlreadyAnswered)
	_, err = db.AddFeudResponse("other", qid, "u4", "Fish", "fish", now)
	assert.ErrorIs(t, err, ErrFeudNotActive, "a question from another guild")

	answers, err := db.FeudAnswers(qid)
	require.NoError(t, err)
	require.Len(t, answers, 2)
	assert.Equal(t, "Dog", answers[0].Label)
	assert.Equal(t, 2, answers[0].Score())
	assert.Equal(t, "Cat", answers[1].Label)

	q, ok, err := db.FeudQuestion("g", qid)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, 3, q.Respondents)
}

func TestFeudQuestionsToAnswerAndPool(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	_, err := db.SeedFeudQuestions("g", []FeudSeed{{"Q1 question", "q1"}, {"Q2 question", "q2"}}, now)
	require.NoError(t, err)
	_, err = db.SubmitFeudQuestion("g", "u", "Q3 question", "q3", 3, now)
	require.NoError(t, err)
	q1 := feudQuestionID(t, db, "g", "q1")
	q2 := feudQuestionID(t, db, "g", "q2")
	for _, u := range []string{"a", "b"} {
		_, err = db.AddFeudResponse("g", q1, u, "x", "x", now)
		require.NoError(t, err)
	}

	qs, err := db.FeudQuestionsToAnswer("g", "a")
	require.NoError(t, err)
	require.Len(t, qs, 1, "answered and pending questions are left out")
	assert.Equal(t, q2, qs[0].ID)

	qs, err = db.FeudQuestionsToAnswer("g", "c")
	require.NoError(t, err)
	require.Len(t, qs, 2)
	assert.Equal(t, 2, qs[0].Respondents)

	st, err := db.FeudPoolStats("g", 2)
	require.NoError(t, err)
	assert.Equal(t, FeudPoolStats{Pending: 1, Collecting: 1, Ready: 1}, st)
	st, err = db.FeudPoolStats("empty", 2)
	require.NoError(t, err)
	assert.Equal(t, FeudPoolStats{}, st)
}
