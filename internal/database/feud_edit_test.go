package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func editFixture(t *testing.T) (*DB, int64, map[string]int64) {
	t.Helper()
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	_, err := db.SeedFeudQuestions("g", []FeudSeed{{"Name a pet.", "name pet"}}, now)
	require.NoError(t, err)
	qid := feudQuestionID(t, db, "g", "name pet")
	ids := map[string]int64{}
	for u, w := range map[string]string{"u1": "Dog", "u2": "Doggo", "u3": "Cat", "u4": "asdf"} {
		a, err := db.AddFeudResponse("g", qid, u, w, normalizeForTest(w), now)
		require.NoError(t, err)
		ids[w] = a.ID
	}
	return db, qid, ids
}

func normalizeForTest(s string) string {
	return map[string]string{"Dog": "dog", "Doggo": "doggo", "Cat": "cat", "asdf": "asdf"}[s]
}

func TestFeudMergeKeepsResponses(t *testing.T) {
	db, qid, ids := editFixture(t)
	require.NoError(t, db.SetFeudAnswerExtra(qid, ids["Doggo"], 2))
	require.NoError(t, db.MergeFeudAnswers(qid, ids["Doggo"], ids["Dog"]))

	a, err := db.FeudAnswerIn("g", ids["Dog"])
	require.NoError(t, err)
	assert.Equal(t, 2, a.Count)
	assert.Equal(t, 2, a.Extra, "bonus moves with the merge")
	_, err = db.FeudAnswerIn("g", ids["Doggo"])
	assert.ErrorIs(t, err, ErrFeudAnswerNotFound)

	aliases, err := db.FeudAnswerAliases(qid)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"dog", "doggo"}, aliases[ids["Dog"]], "typed wording still matches")

	// A newcomer typing the merged wording joins the surviving group.
	b, err := db.AddFeudResponse("g", qid, "u5", "doggo", "doggo", time.Unix(1000, 0))
	require.NoError(t, err)
	assert.Equal(t, ids["Dog"], b.ID)

	assert.ErrorIs(t, db.MergeFeudAnswers(qid, ids["Dog"], ids["Dog"]), ErrFeudAnswerNotFound)
	assert.ErrorIs(t, db.MergeFeudAnswers(qid+1, ids["Cat"], ids["Dog"]), ErrFeudAnswerNotFound, "wrong question")
}

func TestFeudHideRenameAndBonus(t *testing.T) {
	db, qid, ids := editFixture(t)
	require.NoError(t, db.SetFeudAnswerHidden(qid, ids["asdf"], true))
	answers, err := db.FeudAnswers(qid)
	require.NoError(t, err)
	assert.Len(t, answers, 3, "hidden groups stay off the board")
	all, err := db.AllFeudAnswers(qid)
	require.NoError(t, err)
	assert.Len(t, all, 4)

	// Someone typing the junk again doesn't bring it back.
	_, err = db.AddFeudResponse("g", qid, "u9", "asdf", "asdf", time.Unix(1000, 0))
	require.NoError(t, err)
	answers, _ = db.FeudAnswers(qid)
	assert.Len(t, answers, 3)

	require.NoError(t, db.RenameFeudAnswer(qid, ids["Cat"], "Kitty", "kitty"))
	assert.ErrorIs(t, db.RenameFeudAnswer(qid, ids["Cat"], "Dog", "dog"), ErrFeudAnswerExists)
	require.NoError(t, db.SetFeudAnswerExtra(qid, ids["Cat"], 5))
	a, err := db.FeudAnswerIn("g", ids["Cat"])
	require.NoError(t, err)
	assert.Equal(t, "Kitty", a.Label)
	assert.Equal(t, 6, a.Score())

	require.NoError(t, db.SetFeudAnswerExtra(qid, ids["Dog"], -1))
	answers, _ = db.FeudAnswers(qid)
	for _, x := range answers {
		assert.NotEqual(t, ids["Dog"], x.ID, "a score of zero leaves the board")
	}
	_, err = db.FeudAnswerIn("other", ids["Cat"])
	assert.ErrorIs(t, err, ErrFeudAnswerNotFound, "another guild")
}

func TestFeudAddAnswerAndQuestionEdits(t *testing.T) {
	db, qid, _ := editFixture(t)
	id, err := db.AddFeudAnswer(qid, "Fish", "fish", 3, time.Unix(1000, 0))
	require.NoError(t, err)
	a, err := db.FeudAnswerIn("g", id)
	require.NoError(t, err)
	assert.Equal(t, 3, a.Score())
	_, err = db.AddFeudAnswer(qid, "Dog", "dog", 1, time.Unix(1000, 0))
	assert.ErrorIs(t, err, ErrFeudAnswerExists)

	_, err = db.SubmitFeudQuestion("g", "u", "Name a drink.", "name drink", time.Unix(1000, 0))
	require.NoError(t, err)
	assert.ErrorIs(t, db.UpdateFeudQuestionText("g", qid, "Name a drink!", "name drink"), ErrFeudDuplicateQuestion)
	require.NoError(t, db.UpdateFeudQuestionText("g", qid, "Name a pet people keep.", "name pet people keep"))
	assert.ErrorIs(t, db.UpdateFeudQuestionText("other", qid, "Name x", "name x"), ErrFeudQuestionNotFound)

	require.NoError(t, db.SetFeudQuestionRetired("g", qid, true))
	assert.ErrorIs(t, db.SetFeudQuestionRetired("g", qid, true), ErrFeudBadStatus)
	_, err = db.AddFeudResponse("g", qid, "late", "Bird", "bird", time.Unix(1000, 0))
	assert.ErrorIs(t, err, ErrFeudNotActive)
	require.NoError(t, db.SetFeudQuestionRetired("g", qid, false))

	qs, total, err := db.FeudQuestionsPage("g", 0, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, total)
	require.Len(t, qs, 1)
	ids, err := db.FeudActiveQuestionIDs("g", 2)
	require.NoError(t, err)
	assert.Equal(t, []int64{qid}, ids, "only the question with several groups")
}

func TestFeudAnswersMigration(t *testing.T) {
	db := newSquadDB(t)
	_, err := db.conn.Exec(`DROP TABLE feud_answers`)
	require.NoError(t, err)
	_, err = db.conn.Exec(`CREATE TABLE feud_answers (id INTEGER PRIMARY KEY AUTOINCREMENT, question_id INTEGER NOT NULL,
		label TEXT NOT NULL, norm TEXT NOT NULL, extra INTEGER NOT NULL DEFAULT 0, created_at INTEGER NOT NULL)`)
	require.NoError(t, err)
	_, err = db.conn.Exec(`INSERT INTO feud_answers (question_id, label, norm, created_at) VALUES (1, 'Dog', 'dog', 1)`)
	require.NoError(t, err)
	require.NoError(t, db.migrateFeudAnswers())
	require.NoError(t, db.migrateFeudAnswers(), "safe to repeat")
	all, err := db.AllFeudAnswers(1)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.False(t, all[0].Hidden)
}
