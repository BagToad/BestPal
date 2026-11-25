package feud

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var t0 = time.Unix(10_000, 0)

func heads(int) int { return 0 }

func testSlots(labels ...string) []Slot {
	out := make([]Slot, len(labels))
	for n, l := range labels {
		out[n] = Slot{AnswerID: int64(n + 1), Label: l, Points: 10 - n, Aliases: []string{normalize(l)}, By: -1}
	}
	return out
}

// playingGame has red players a, b, c and blue players x, y, with red to
// guess on a 3-answer board.
func playingGame(t *testing.T) *Game {
	t.Helper()
	g := newGame("g", "mod", 2, time.Minute)
	coins := []int{red, blue, red, blue, red}
	for n, uid := range []string{"a", "x", "b", "y", "c"} {
		team, err := g.join(uid, t0, func(int) int { return coins[n] })
		require.NoError(t, err)
		require.Equal(t, coins[n], team)
	}
	g.startRound(1, "Name a pet", testSlots("Dog", "Cat", "Fish"), t0, heads)
	require.Equal(t, phaseTurn, g.Phase)
	require.Equal(t, red, g.Turn)
	return g
}

func TestJoinBalancesTeams(t *testing.T) {
	g := newGame("g", "mod", 3, time.Minute)
	team, err := g.join("a", t0, func(int) int { return blue })
	require.NoError(t, err)
	assert.Equal(t, blue, team, "a coin flip decides ties")
	team, _ = g.join("b", t0, func(int) int { return blue })
	assert.Equal(t, red, team, "the smaller team gets the next player")
	_, err = g.join("a", t0, heads)
	assert.ErrorIs(t, err, errAlreadyIn)
	assert.Equal(t, t0.Add(lobbyWait), g.Deadline, "the countdown starts once both teams have someone")

	_, err = g.leave("a", t0)
	require.NoError(t, err)
	assert.True(t, g.Deadline.IsZero(), "and stops if a team empties")
	_, err = g.join("a", t0, heads)
	assert.NoError(t, err, "leaving in the lobby doesn't block rejoining")
}

func TestLeaverWaitsForRoundToEnd(t *testing.T) {
	g := playingGame(t)
	_, err := g.leave("a", t0)
	require.NoError(t, err)
	_, err = g.join("a", t0, heads)
	assert.ErrorIs(t, err, errWaitRound)

	// Finish round 1: both teams strike out.
	for g.Phase == phaseTurn {
		g.timeUp(t0)
	}
	require.Equal(t, phaseBreak, g.Phase)
	_, err = g.join("a", t0, heads)
	assert.NoError(t, err, "the round is over")
}

func TestEmptyTeamPausesAndResumes(t *testing.T) {
	g := playingGame(t)
	for _, uid := range []string{"x", "y"} {
		_, err := g.leave(uid, t0)
		require.NoError(t, err)
	}
	assert.Equal(t, phasePaused, g.Phase)
	assert.Equal(t, t0.Add(pauseWait), g.Deadline)
	seq := g.TurnSeq

	team, err := g.join("z", t0, heads)
	require.NoError(t, err)
	assert.Equal(t, blue, team)
	assert.Equal(t, phaseTurn, g.Phase, "a new player resumes the game")
	assert.Equal(t, seq+1, g.TurnSeq, "with a fresh turn")
}

func TestMajorityLocksIn(t *testing.T) {
	g := playingGame(t)
	seq := g.TurnSeq
	locked, err := g.suggest("a", "Doggo", "doggo", seq)
	require.NoError(t, err)
	assert.Empty(t, locked, "1 of 3 isn't a majority")
	_, err = g.suggest("x", "Cat", "cat", seq)
	assert.ErrorIs(t, err, errNotYourTurn)
	_, err = g.suggest("b", "Cat", "cat", seq)
	require.NoError(t, err)
	locked, err = g.suggest("c", "cats", "cat", seq)
	assert.ErrorIs(t, err, errDuplicateIdea, "the same idea joins the existing suggestion")
	assert.Equal(t, "Cat", locked)
	assert.Equal(t, phaseJudging, g.Phase)
	_, err = g.vote("a", 1, seq)
	assert.ErrorIs(t, err, errNoTurn, "no more votes once locked")
}

func TestVoteSwitchingAndStaleTurns(t *testing.T) {
	g := playingGame(t)
	seq := g.TurnSeq
	_, _ = g.suggest("a", "Dog", "dog", seq)
	_, _ = g.suggest("b", "Cat", "cat", seq)
	locked, err := g.vote("a", 2, seq)
	require.NoError(t, err)
	assert.Equal(t, "Cat", locked, "a switched vote makes 2 of 3")
	_, err = g.vote("c", 1, seq-1)
	assert.ErrorIs(t, err, errNoTurn)
}

func TestSoloPlayerLocksImmediately(t *testing.T) {
	g := newGame("g", "mod", 1, time.Minute)
	_, _ = g.join("a", t0, func(int) int { return red })
	_, _ = g.join("x", t0, heads)
	g.startRound(1, "Q", testSlots("Dog"), t0, heads)
	locked, err := g.suggest("a", "Dog", "dog", g.TurnSeq)
	require.NoError(t, err)
	assert.Equal(t, "Dog", locked)
}

func TestTimeUpPicksTopSuggestionOrStrikes(t *testing.T) {
	g := playingGame(t)
	assert.Empty(t, g.timeUp(t0), "no suggestions is a strike")
	assert.Equal(t, 1, g.Strikes[red])
	assert.Equal(t, blue, g.Turn, "then the other team goes")

	seq := g.TurnSeq
	_, _ = g.suggest("x", "Fish", "fish", seq)
	_, _ = g.suggest("y", "Cat", "cat", seq)
	assert.Equal(t, "Fish", g.timeUp(t0), "ties go to the earliest suggestion")
}

func TestScoringAndTurnOrder(t *testing.T) {
	g := playingGame(t)
	g.Phase, g.Locked = phaseJudging, "Doggy"
	g.applyGuess("Doggy", 0, t0)
	assert.Equal(t, 10, g.Score[red])
	assert.True(t, g.Slots[0].Shown)
	assert.Equal(t, red, g.Slots[0].By)
	assert.Equal(t, blue, g.Turn, "turns alternate after a hit too")
	assert.Contains(t, g.Last, "Dog **+10**")

	g.applyGuess("dog", 0, t0)
	assert.Equal(t, 1, g.Strikes[blue], "repeating a shown answer is a strike")
	assert.Equal(t, 0, g.Score[blue])

	assert.Equal(t, red, g.Turn)

	// Blue has struck out, so red keeps guessing.
	g.Strikes[blue] = maxStrikes
	g.applyGuess("Cat", 1, t0)
	assert.Equal(t, 19, g.Score[red])
	assert.Equal(t, red, g.Turn)

	// Clearing the board ends the round and reveals nothing new.
	g.applyGuess("Fish", 2, t0)
	assert.Equal(t, phaseBreak, g.Phase)
	assert.Equal(t, 1, g.Round)
}

func TestBothTeamsStrikeOutRevealsBoard(t *testing.T) {
	g := playingGame(t)
	for range 2 * maxStrikes {
		require.Equal(t, phaseTurn, g.Phase)
		g.timeUp(t0)
	}
	assert.Equal(t, phaseBreak, g.Phase)
	for _, s := range g.Slots {
		assert.True(t, s.Shown)
		assert.Equal(t, -1, s.By)
	}

	g.startRound(2, "Q2", testSlots("A"), t0, heads)
	assert.Equal(t, blue, g.Turn, "the other team starts round 2")
	assert.Equal(t, [2]int{}, g.Strikes)
	g.applyGuess("A", 0, t0)
	assert.Equal(t, phaseEnded, g.Phase, "last round over")
	assert.Equal(t, "🔵 Blue wins!", g.Result)
}

func TestMatchExactUsesAliases(t *testing.T) {
	g := playingGame(t)
	g.Slots[1].Aliases = append(g.Slots[1].Aliases, "kitty")
	assert.Equal(t, 1, g.matchExact("kitty"))
	assert.Equal(t, -1, g.matchExact("hamster"))
}

func TestParseSlot(t *testing.T) {
	assert.Equal(t, 1, parseSlot("2", 3))
	assert.Equal(t, 0, parseSlot("Answer: 1.", 3))
	assert.Equal(t, -1, parseSlot("0", 3))
	assert.Equal(t, -1, parseSlot("4", 3))
	assert.Equal(t, -1, parseSlot("none", 3))
}
