package feud

import (
	"errors"
	"slices"
	"strconv"
	"time"
)

// The game engine: pure state changes with no Discord or database calls, so
// the rules are easy to test. The module loads a Game, changes it under the
// guild lock, saves it, and then updates Discord to match.

// Teams.
const (
	red  = 0
	blue = 1
)

var teamNames = [2]string{"Red", "Blue"}

// Phases.
const (
	phaseLobby   = "lobby"   // waiting for both teams to have someone
	phaseTurn    = "turn"    // a team is choosing a guess
	phaseJudging = "judging" // a guess is locked in and being checked
	phaseBreak   = "break"   // between rounds
	phasePaused  = "paused"  // a team is empty
	phaseEnded   = "ended"
)

const (
	maxStrikes  = 3
	boardSlots  = 10
	maxSuggests = 20
)

// Timings.
const (
	lobbyWait   = 30 * time.Second
	breakWait   = 20 * time.Second
	pauseWait   = 2 * time.Minute
	judgeWait   = 45 * time.Second // safety net if a check never finishes
	resumeBreak = 5 * time.Second
)

var (
	errNotInGame     = errors.New("not in the game")
	errAlreadyIn     = errors.New("already in the game")
	errWaitRound     = errors.New("wait for the round to end")
	errNotYourTurn   = errors.New("not your team's turn")
	errNoTurn        = errors.New("no turn in progress")
	errStale         = errors.New("that turn is over")
	errTooMany       = errors.New("too many suggestions")
	errGameOver      = errors.New("game over")
	errNoSuggestion  = errors.New("no such suggestion")
	errDuplicateIdea = errors.New("already suggested")
)

// Game is a live GP Feud game.
type Game struct {
	ID        int64  `json:"-"`
	GuildID   string `json:"guild"`
	GameNo    int64  `json:"game_no"`
	StartedBy string `json:"started_by"`
	Phase     string `json:"phase"`
	// PausedFrom is the phase a pause interrupted.
	PausedFrom string    `json:"paused_from,omitempty"`
	Deadline   time.Time `json:"deadline"`

	Rounds     int     `json:"rounds"`
	Round      int     `json:"round"` // 0 in the lobby
	Played     []int64 `json:"played"`
	Question   string  `json:"question"`
	QuestionID int64   `json:"question_id"`
	Slots      []Slot  `json:"slots"`

	Score        [2]int        `json:"score"`
	Strikes      [2]int        `json:"strikes"`
	Turn         int           `json:"turn"`
	RoundStarter int           `json:"round_starter"`
	TurnSeq      int           `json:"turn_seq"`
	TurnTimer    time.Duration `json:"turn_timer"`

	Players     []Player       `json:"players"`
	Suggestions []Suggestion   `json:"suggestions"`
	Votes       map[string]int `json:"votes"` // user -> suggestion ID
	NextSuggest int            `json:"next_suggest"`
	Locked      string         `json:"locked,omitempty"` // the guess being judged

	// Panel is the current turn's message in the team thread.
	PanelChannel string `json:"panel_channel,omitempty"`
	PanelMessage string `json:"panel_message,omitempty"`

	Last   string `json:"last,omitempty"`   // what just happened, for the board
	Result string `json:"result,omitempty"` // how the game ended
}

// Slot is one answer on the board.
type Slot struct {
	AnswerID int64    `json:"answer_id"`
	Label    string   `json:"label"`
	Points   int      `json:"points"`
	Aliases  []string `json:"aliases"`
	Shown    bool     `json:"shown"`
	By       int      `json:"by"` // team that found it, or -1
}

// Player is someone who joined.
type Player struct {
	UserID    string `json:"user"`
	Team      int    `json:"team"`
	Active    bool   `json:"active"`
	LeftRound int    `json:"left_round"`
}

// Suggestion is a possible guess in the current turn.
type Suggestion struct {
	ID     int    `json:"id"`
	UserID string `json:"user"`
	Text   string `json:"text"`
	Norm   string `json:"norm"`
}

func newGame(guildID, startedBy string, rounds int, timer time.Duration) *Game {
	return &Game{GuildID: guildID, StartedBy: startedBy, Phase: phaseLobby, Rounds: rounds, TurnTimer: timer, Votes: map[string]int{}}
}

func (g *Game) player(uid string) *Player {
	for n := range g.Players {
		if g.Players[n].UserID == uid {
			return &g.Players[n]
		}
	}
	return nil
}

func (g *Game) members(team int) []string {
	var out []string
	for _, p := range g.Players {
		if p.Active && p.Team == team {
			out = append(out, p.UserID)
		}
	}
	return out
}

func (g *Game) bothTeams() bool { return len(g.members(red)) > 0 && len(g.members(blue)) > 0 }

// playing reports whether a round is underway (rather than the lobby, a
// break or the end).
func (g *Game) playing() bool {
	switch g.Phase {
	case phaseTurn, phaseJudging:
		return true
	case phasePaused:
		return g.PausedFrom == phaseTurn || g.PausedFrom == phaseJudging
	}
	return false
}

// join adds a player to the smaller team (coin flip on ties). Someone who
// left can't come back until the round they left in is over.
func (g *Game) join(uid string, now time.Time, coin func(n int) int) (int, error) {
	if g.Phase == phaseEnded {
		return 0, errGameOver
	}
	p := g.player(uid)
	if p != nil && p.Active {
		return p.Team, errAlreadyIn
	}
	if p != nil && p.LeftRound == g.Round && g.playing() {
		return 0, errWaitRound
	}
	team := red
	r, b := len(g.members(red)), len(g.members(blue))
	switch {
	case b < r:
		team = blue
	case b == r:
		team = coin(2)
	}
	if p == nil {
		g.Players = append(g.Players, Player{UserID: uid})
		p = &g.Players[len(g.Players)-1]
	}
	p.Team, p.Active, p.LeftRound = team, true, 0
	g.teamsChanged(now)
	return team, nil
}

// leave takes a player out of the game for the rest of the round.
func (g *Game) leave(uid string, now time.Time) (int, error) {
	p := g.player(uid)
	if p == nil || !p.Active {
		return 0, errNotInGame
	}
	p.Active, p.LeftRound = false, g.Round
	delete(g.Votes, uid)
	g.teamsChanged(now)
	return p.Team, nil
}

// teamsChanged starts the lobby countdown, pauses or resumes as teams fill
// and empty. It reports whether a new turn started.
func (g *Game) teamsChanged(now time.Time) bool {
	full := g.bothTeams()
	switch {
	case g.Phase == phaseLobby && full && g.Deadline.IsZero():
		g.Deadline = now.Add(lobbyWait)
	case g.Phase == phaseLobby && !full:
		g.Deadline = time.Time{}
	case g.Phase == phasePaused && full:
		from := g.PausedFrom
		g.PausedFrom = ""
		if from == phaseBreak {
			g.Phase, g.Deadline = phaseBreak, now.Add(resumeBreak)
			return false
		}
		g.startTurn(g.Turn, now)
		return true
	case !full && (g.Phase == phaseTurn || g.Phase == phaseJudging || g.Phase == phaseBreak):
		g.PausedFrom, g.Phase, g.Deadline = g.Phase, phasePaused, now.Add(pauseWait)
		g.Locked = ""
	}
	return false
}

// startRound puts a new question on the board. Teams take turns starting
// rounds.
func (g *Game) startRound(qid int64, text string, slots []Slot, now time.Time, coin func(n int) int) {
	g.Round++
	g.Played = append(g.Played, qid)
	g.QuestionID, g.Question, g.Slots = qid, text, slots
	g.Strikes = [2]int{}
	if g.Round == 1 {
		g.RoundStarter = coin(2)
	} else {
		g.RoundStarter = 1 - g.RoundStarter
	}
	g.Last = ""
	g.startTurn(g.RoundStarter, now)
}

// startTurn hands the board to a team.
func (g *Game) startTurn(team int, now time.Time) {
	g.Phase, g.Turn = phaseTurn, team
	g.TurnSeq++
	g.Suggestions, g.Votes, g.Locked = nil, map[string]int{}, ""
	g.Deadline = now.Add(g.turnLength())
	g.PanelChannel, g.PanelMessage = "", ""
}

// turnLength is stored on the game so restarts keep the timer.
func (g *Game) turnLength() time.Duration {
	if g.TurnTimer > 0 {
		return g.TurnTimer
	}
	return 90 * time.Second
}

// suggest adds a possible guess and moves the suggester's vote to it (or to
// the same idea someone already had). It reports what was locked in, if the
// vote made a majority.
func (g *Game) suggest(uid, text, norm string, seq int) (locked string, err error) {
	if err := g.canVote(uid, seq); err != nil {
		return "", err
	}
	for _, s := range g.Suggestions {
		if s.Norm == norm {
			g.Votes[uid] = s.ID
			return g.checkMajority(), errDuplicateIdea
		}
	}
	if len(g.Suggestions) >= maxSuggests {
		return "", errTooMany
	}
	g.NextSuggest++
	g.Suggestions = append(g.Suggestions, Suggestion{ID: g.NextSuggest, UserID: uid, Text: text, Norm: norm})
	g.Votes[uid] = g.NextSuggest
	return g.checkMajority(), nil
}

// vote moves a player's vote.
func (g *Game) vote(uid string, id, seq int) (locked string, err error) {
	if err := g.canVote(uid, seq); err != nil {
		return "", err
	}
	if !slices.ContainsFunc(g.Suggestions, func(s Suggestion) bool { return s.ID == id }) {
		return "", errNoSuggestion
	}
	g.Votes[uid] = id
	return g.checkMajority(), nil
}

func (g *Game) canVote(uid string, seq int) error {
	if g.Phase != phaseTurn {
		return errNoTurn
	}
	if seq != g.TurnSeq {
		return errStale
	}
	p := g.player(uid)
	if p == nil || !p.Active {
		return errNotInGame
	}
	if p.Team != g.Turn {
		return errNotYourTurn
	}
	return nil
}

// tally counts each suggestion's votes from players still on the team.
func (g *Game) tally() map[int]int {
	out := map[int]int{}
	for uid, id := range g.Votes {
		if p := g.player(uid); p != nil && p.Active && p.Team == g.Turn {
			out[id]++
		}
	}
	return out
}

// checkMajority locks in a suggestion more than half the team backs.
func (g *Game) checkMajority() string {
	team := len(g.members(g.Turn))
	for id, n := range g.tally() {
		if n*2 > team {
			return g.lock(id)
		}
	}
	return ""
}

// topSuggestion is the most-voted suggestion, earliest on ties.
func (g *Game) topSuggestion() (Suggestion, bool) {
	votes := g.tally()
	best, found := Suggestion{}, false
	for _, s := range g.Suggestions {
		if !found || votes[s.ID] > votes[best.ID] {
			best, found = s, true
		}
	}
	return best, found
}

func (g *Game) lock(id int) string {
	for _, s := range g.Suggestions {
		if s.ID == id {
			g.Phase, g.Locked = phaseJudging, s.Text
			return s.Text
		}
	}
	return ""
}

// timeUp ends a turn: the top suggestion is locked in, or with none the
// team takes a strike. It reports the locked guess, if any.
func (g *Game) timeUp(now time.Time) string {
	if s, ok := g.topSuggestion(); ok {
		return g.lock(s.ID)
	}
	g.Last = teamNames[g.Turn] + " ran out of time ✖"
	g.miss(now)
	return ""
}

// matchExact finds the slot whose answer or aliases match the guess exactly.
func (g *Game) matchExact(norm string) int {
	for n, s := range g.Slots {
		if slices.Contains(s.Aliases, norm) {
			return n
		}
	}
	return -1
}

// applyGuess scores the judged guess (slot -1 for a miss).
func (g *Game) applyGuess(guess string, slot int, now time.Time) {
	team := g.Turn
	switch {
	case slot < 0 || slot >= len(g.Slots):
		g.Last = teamNames[team] + " guessed **" + guess + "** ✖"
		g.miss(now)
	case g.Slots[slot].Shown:
		g.Last = teamNames[team] + " guessed **" + guess + "**, already on the board ✖"
		g.miss(now)
	default:
		s := &g.Slots[slot]
		s.Shown, s.By = true, team
		g.Score[team] += s.Points
		g.Last = teamNames[team] + " guessed **" + guess + "** → " + s.Label + " **+" + strconv.Itoa(s.Points) + "**"
		g.next(now)
	}
}

func (g *Game) miss(now time.Time) {
	g.Strikes[g.Turn]++
	g.next(now)
}

// next passes the board to the other team, unless it's struck out, or ends
// the round when the board is clear or both teams are out.
func (g *Game) next(now time.Time) {
	cleared := !slices.ContainsFunc(g.Slots, func(s Slot) bool { return !s.Shown })
	other := 1 - g.Turn
	switch {
	case cleared:
		g.endRound(now)
	case g.Strikes[other] < maxStrikes:
		g.startTurn(other, now)
	case g.Strikes[g.Turn] < maxStrikes:
		g.startTurn(g.Turn, now)
	default:
		g.endRound(now)
	}
}

// endRound reveals what nobody found and takes a break, or ends the game
// after the last round.
func (g *Game) endRound(now time.Time) {
	for n := range g.Slots {
		if !g.Slots[n].Shown {
			g.Slots[n].Shown, g.Slots[n].By = true, -1
		}
	}
	g.Locked = ""
	if g.Round >= g.Rounds {
		g.finish(now, "")
		return
	}
	g.Phase, g.Deadline = phaseBreak, now.Add(breakWait)
}

// finish ends the game. reason explains an early end.
func (g *Game) finish(_ time.Time, reason string) {
	g.Phase, g.Deadline, g.Locked = phaseEnded, time.Time{}, ""
	switch {
	case reason != "":
		g.Result = reason
	case g.Score[red] > g.Score[blue]:
		g.Result = "🔴 Red wins!"
	case g.Score[blue] > g.Score[red]:
		g.Result = "🔵 Blue wins!"
	default:
		g.Result = "It's a tie!"
	}
}
