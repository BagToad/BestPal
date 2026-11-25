package database

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFeudGameLifecycle(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	id, n, err := db.CreateFeudGame("g", `{"phase":"lobby"}`, now)
	require.NoError(t, err)
	assert.Equal(t, int64(1), n)
	_, _, err = db.CreateFeudGame("g", `{}`, now)
	assert.ErrorIs(t, err, ErrFeudGameRunning, "one unfinished game per guild")
	_, _, err = db.CreateFeudGame("other", `{}`, now)
	require.NoError(t, err)

	live, err := db.LiveFeudGames()
	require.NoError(t, err)
	assert.Len(t, live, 2)

	require.NoError(t, db.SaveFeudGame(id, `{"phase":"ended"}`, true, now))
	_, ok, err := db.LiveFeudGame("g")
	require.NoError(t, err)
	assert.False(t, ok)
	last, ok, err := db.LastFeudGame("g")
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, last.Ended)
	assert.Equal(t, `{"phase":"ended"}`, last.State)

	_, n, err = db.CreateFeudGame("g", `{}`, now)
	require.NoError(t, err)
	assert.Equal(t, int64(2), n, "game numbers count up per guild")
}

func TestPickFeudQuestionCooldown(t *testing.T) {
	db := newSquadDB(t)
	now := time.Unix(1000, 0)
	_, err := db.SeedFeudQuestions("g", []FeudSeed{{"Q1 question", "q1"}, {"Q2 question", "q2"}, {"Q3 question", "q3"}}, now)
	require.NoError(t, err)
	ids := map[string]int64{}
	for _, k := range []string{"q1", "q2", "q3"} {
		ids[k] = feudQuestionID(t, db, "g", k)
	}
	for _, k := range []string{"q1", "q2"} {
		_, err := db.AddFeudResponse("g", ids[k], "u", "x", "x", now)
		require.NoError(t, err)
	}
	first := func(int) int { return 0 }

	q, ok, err := db.PickFeudQuestion("g", 1, 50, 1, nil, first)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, ids["q1"], q.ID, "q3 has no answers")

	require.NoError(t, db.MarkFeudQuestionPlayed(ids["q1"], 1))
	q, _, err = db.PickFeudQuestion("g", 1, 50, 2, nil, first)
	require.NoError(t, err)
	assert.Equal(t, ids["q2"], q.ID, "q1 is cooling down")

	require.NoError(t, db.MarkFeudQuestionPlayed(ids["q2"], 2))
	q, _, err = db.PickFeudQuestion("g", 1, 50, 3, nil, first)
	require.NoError(t, err)
	assert.Equal(t, ids["q1"], q.ID, "all cooling down: the longest-rested one")
	q, _, err = db.PickFeudQuestion("g", 1, 50, 3, []int64{ids["q1"]}, first)
	require.NoError(t, err)
	assert.Equal(t, ids["q2"], q.ID, "never repeat within a game")
	_, ok, err = db.PickFeudQuestion("g", 1, 50, 3, []int64{ids["q1"], ids["q2"]}, first)
	require.NoError(t, err)
	assert.False(t, ok)

	q, _, err = db.PickFeudQuestion("g", 1, 1, 3, nil, first)
	require.NoError(t, err)
	assert.Equal(t, ids["q1"], q.ID, "a short cooldown frees q1 again")

	got, _, err := db.FeudQuestion("g", ids["q1"])
	require.NoError(t, err)
	assert.Equal(t, 1, got.TimesPlayed)
	assert.Equal(t, int64(1), got.LastPlayedGame)

	aliases, err := db.FeudAnswerAliases(ids["q1"])
	require.NoError(t, err)
	assert.Len(t, aliases, 1)
}
