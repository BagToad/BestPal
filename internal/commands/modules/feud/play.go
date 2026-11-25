package feud

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// Gameplay: the board, team threads, turns and timers. Every change to a game
// goes through mutate, which holds the guild lock while the game is loaded,
// changed and saved, then brings Discord up to date outside the lock.

const (
	suggestInputID = "guess"
	maxGuessLen    = 60
	llmTimeout     = 20 * time.Second
)

// guildLock serializes changes to one guild's game.
func (m *Module) guildLock(guildID string) func() {
	m.mu.Lock()
	l, ok := m.locks[guildID]
	if !ok {
		l = &chanMutex{ch: make(chan struct{}, 1)}
		m.locks[guildID] = l
	}
	m.mu.Unlock()
	l.ch <- struct{}{}
	return func() { <-l.ch }
}

type chanMutex struct{ ch chan struct{} }

func (m *Module) loadGame(guildID string) (*Game, error) {
	row, ok, err := m.db.LiveFeudGame(guildID)
	if err != nil || !ok {
		return nil, err
	}
	return decodeGame(row)
}

func decodeGame(row database.FeudGameRow) (*Game, error) {
	g := &Game{}
	if err := json.Unmarshal([]byte(row.State), g); err != nil {
		return nil, fmt.Errorf("decode feud game %d: %w", row.ID, err)
	}
	g.ID = row.ID
	if g.Votes == nil {
		g.Votes = map[string]int{}
	}
	return g, nil
}

func (m *Module) saveGame(g *Game) error {
	b, err := json.Marshal(g)
	if err != nil {
		return err
	}
	return m.db.SaveFeudGame(g.ID, string(b), g.Phase == phaseEnded, m.now())
}

// errNoGame means the guild has no game running.
var errNoGame = errors.New("no game running")

// mutate changes the live game. fn's error cancels the save. reply, if
// set, runs next (with the error, if any) so interactions are answered
// quickly; then Discord is brought up to date.
func (m *Module) mutate(guildID string, fn func(g *Game) error, reply func(g *Game, err error)) {
	g, before, err := m.change(guildID, fn)
	if reply != nil {
		reply(g, err)
	}
	if err == nil {
		m.sync(before, g)
	}
}

func (m *Module) change(guildID string, fn func(g *Game) error) (*Game, *Game, error) {
	unlock := m.guildLock(guildID)
	defer unlock()
	g, err := m.loadGame(guildID)
	if err == nil && g == nil {
		err = errNoGame
	}
	if err != nil {
		return nil, nil, err
	}
	before := *g
	before.Players = append([]Player(nil), g.Players...)
	if err := fn(g); err != nil {
		return g, nil, err
	}
	if err := m.saveGame(g); err != nil {
		return nil, nil, err
	}
	return g, &before, nil
}

// sync updates Discord after a change: thread membership, the turn panel,
// the board and the timer. It runs under its own lock and works from the
// newest saved state, so changes finishing out of order can't leave Discord
// showing an older one.
func (m *Module) sync(before, g *Game) {
	unlock := m.guildLock(g.GuildID + ":sync")
	defer unlock()
	board, ok, err := m.db.FeudBoard(g.GuildID)
	if err != nil || !ok {
		if err != nil {
			m.config.Logger.Warnf("feud: load board: %v", err)
		}
		return
	}
	latest := func() *Game {
		if row, ok, err := m.db.LastFeudGame(g.GuildID); err == nil && ok {
			if cur, err := decodeGame(row); err == nil {
				return cur
			}
		}
		return g
	}
	threads := [2]string{board.RedThreadID, board.BlueThreadID}
	m.syncThreads(before, g, latest(), threads)
	switch {
	case before.TurnSeq != g.TurnSeq || (g.Phase != phaseTurn && g.Phase != phaseJudging):
		m.closePanel(before, g)
	case g.Phase == phaseJudging && before.Phase == phaseTurn:
		m.editPanel(panelRef(before, latest()), lockedContent(g.Locked))
	}
	if g.Phase == phaseTurn && (before.TurnSeq != g.TurnSeq || before.Phase != phaseTurn) && g.PanelMessage == "" {
		m.postPanel(g, threads[g.Turn])
	}
	cur := latest()
	if g.Phase == phaseTurn && before.Phase == phaseTurn && before.TurnSeq == g.TurnSeq && !samePanel(before, g) {
		m.refreshPanel(cur, g.TurnSeq)
	}
	if !sameBoard(before, cur, board) {
		m.refreshBoard(board, cur)
	}
	m.arm(cur.GuildID, cur.Deadline)
}

// sameBoard reports whether a change left the board looking the same, as
// votes and suggestions do.
func sameBoard(before, g *Game, board database.FeudBoard) bool {
	a, errA := json.Marshal(boardEmbed(before, board))
	b, errB := json.Marshal(boardEmbed(g, board))
	return errA == nil && errB == nil && string(a) == string(b)
}

// samePanel reports whether a change left the turn panel looking the same.
func samePanel(before, g *Game) bool {
	a, errA := json.Marshal(panelComponents(before))
	b, errB := json.Marshal(panelComponents(g))
	return errA == nil && errB == nil && string(a) == string(b)
}

// syncThreads brings the team threads in line with the players a change
// touched. What each thread should hold comes from cur, the newest saved
// state, not from the change itself: a Join finishing after a later Leave
// must not put the leaver back. At the end of a game everyone is removed,
// ready for the next one.
func (m *Module) syncThreads(before, g, cur *Game, threads [2]string) {
	was := map[string]Player{}
	for _, p := range before.Players {
		if p.Active {
			was[p.UserID] = p
		}
	}
	over := g.Phase == phaseEnded && before.Phase != phaseEnded
	api := m.dc(nil)
	for _, p := range g.Players {
		old, wasIn := was[p.UserID]
		if !over && wasIn == p.Active && (!wasIn || old.Team == p.Team) {
			continue
		}
		want := cur.player(p.UserID)
		stay := want != nil && want.Active && cur.Phase != phaseEnded
		switch {
		case stay:
			if wasIn && old.Team == want.Team {
				continue
			}
			if err := api.AddThreadMember(threads[want.Team], p.UserID); err != nil {
				m.config.Logger.Warnf("feud: add %s to thread: %v", p.UserID, err)
			}
			if _, err := api.Send(threads[want.Team], &discordgo.MessageSend{
				Content:         fmt.Sprintf("%s <@%s> joined **%s**! Plan your answers here with your team.", teamEmoji(want.Team), p.UserID, teamNames[want.Team]),
				AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{p.UserID}},
			}); err != nil {
				m.config.Logger.Warnf("feud: welcome %s: %v", p.UserID, err)
			}
		default:
			team := p.Team
			if wasIn {
				team = old.Team
			}
			if err := api.RemoveThreadMember(threads[team], p.UserID); err != nil {
				m.config.Logger.Warnf("feud: remove %s from thread: %v", p.UserID, err)
			}
		}
	}
	if over {
		for team, th := range threads {
			if _, err := api.Send(th, &discordgo.MessageSend{Content: "🏁 **Game over.** " + g.Result + fmt.Sprintf(" (🔴 %d · 🔵 %d)", g.Score[red], g.Score[blue]), AllowedMentions: &discordgo.MessageAllowedMentions{}}); err != nil {
				m.config.Logger.Warnf("feud: game over in %s thread: %v", teamNames[team], err)
			}
		}
	}
}

// postPanel posts the turn panel in the team's thread.
func (m *Module) postPanel(g *Game, threadID string) {
	msg, err := m.dc(nil).Send(threadID, &discordgo.MessageSend{
		Content:         panelContent(g),
		Components:      panelComponents(g),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		m.config.Logger.Warnf("feud: post turn panel: %v", err)
		return
	}
	seq := g.TurnSeq
	if _, _, err := m.change(g.GuildID, func(cur *Game) error {
		if cur.TurnSeq != seq || cur.Phase != phaseTurn {
			return errStale
		}
		cur.PanelChannel, cur.PanelMessage = threadID, msg.ID
		return nil
	}); err != nil && !errors.Is(err, errStale) {
		m.config.Logger.Warnf("feud: save turn panel: %v", err)
	}
}

// closePanel retires the previous turn's panel.
func (m *Module) closePanel(before, g *Game) {
	content := "Turn over."
	if g.Last != "" {
		content = "Turn over: " + g.Last
	}
	m.editPanel(panelRef(before, nil), content)
}

// panelMessage is a turn panel's place in a thread.
type panelMessage struct{ channel, id string }

// panelRef finds the panel of before's turn. If it hadn't been saved when
// before was loaded, the newest state may have it.
func panelRef(before, latest *Game) panelMessage {
	if before.PanelMessage == "" && latest != nil && latest.TurnSeq == before.TurnSeq {
		return panelMessage{latest.PanelChannel, latest.PanelMessage}
	}
	return panelMessage{before.PanelChannel, before.PanelMessage}
}

// refreshPanel redraws the turn panel from the newest state, if it is still
// the same turn.
func (m *Module) refreshPanel(cur *Game, seq int) {
	if cur.TurnSeq != seq || cur.Phase != phaseTurn || cur.PanelMessage == "" {
		return
	}
	content := panelContent(cur)
	comps := panelComponents(cur)
	if _, err := m.dc(nil).Edit(&discordgo.MessageEdit{
		Channel: cur.PanelChannel, ID: cur.PanelMessage, Content: &content, Components: &comps,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("feud: update turn panel: %v", err)
	}
}

func lockedContent(guess string) string {
	return fmt.Sprintf("🔒 **%s** is locked in! Checking the board…", guess)
}

// editPanel replaces a turn panel with a closing note.
func (m *Module) editPanel(ref panelMessage, content string) {
	if ref.id == "" {
		return
	}
	comps := []discordgo.MessageComponent{}
	if _, err := m.dc(nil).Edit(&discordgo.MessageEdit{
		Channel: ref.channel, ID: ref.id, Content: &content, Components: &comps,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("feud: close turn panel: %v", err)
	}
}

func (m *Module) refreshBoard(board database.FeudBoard, g *Game) {
	embeds := []*discordgo.MessageEmbed{boardEmbed(g, board)}
	comps := boardComponents()
	if _, err := m.dc(nil).Edit(&discordgo.MessageEdit{
		Channel: board.ChannelID, ID: board.MessageID, Embeds: &embeds, Components: &comps,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("feud: update board: %v", err)
	}
}

// arm sets the guild's timer for the game's next deadline.
func (m *Module) arm(guildID string, deadline time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if stop, ok := m.timers[guildID]; ok {
		stop()
		delete(m.timers, guildID)
	}
	if deadline.IsZero() {
		return
	}
	wait := max(deadline.Sub(m.now()), 0) + 100*time.Millisecond
	m.timers[guildID] = m.afterFunc(wait, func() { m.tick(guildID) })
}

// tick handles a passed deadline.
func (m *Module) tick(guildID string) {
	var judge string
	var seq int
	var err error
	m.mutate(guildID, func(g *Game) error {
		now := m.now()
		if g.Deadline.IsZero() || now.Before(g.Deadline) {
			return errStale
		}
		switch g.Phase {
		case phaseLobby, phaseBreak:
			return m.nextRound(g, now)
		case phaseTurn:
			judge, seq = g.timeUp(now), g.TurnSeq
		case phaseJudging:
			g.applyGuess(g.Locked, -1, now)
		case phasePaused:
			g.finish(now, "Game ended: a team was empty for too long.")
		}
		return nil
	}, func(_ *Game, e error) { err = e })
	switch {
	case errors.Is(err, errStale):
		if g, _ := m.loadGame(guildID); g != nil {
			m.arm(guildID, g.Deadline)
		}
	case errors.Is(err, errNoGame):
	case err != nil:
		m.config.Logger.Warnf("feud: tick: %v", err)
	case judge != "":
		m.spawn(func() { m.judge(guildID, seq, judge) })
	}
}

// nextRound puts the next question up, or ends the game when there are no
// rounds or questions left.
func (m *Module) nextRound(g *Game, now time.Time) error {
	if g.Round >= g.Rounds {
		g.finish(now, "")
		return nil
	}
	gc := m.config.ForGuild(g.GuildID)
	q, ok, err := m.db.PickFeudQuestion(g.GuildID, gc.GetFeudMinRespondents(), int64(gc.GetFeudQuestionCooldown()), g.GameNo, g.Played, m.randN)
	if err != nil {
		return err
	}
	if !ok {
		if g.Round == 0 {
			g.finish(now, "Game ended: no questions are ready.")
		} else {
			g.finish(now, "")
		}
		return nil
	}
	slots, err := m.boardSlots(q.ID)
	if err != nil {
		return err
	}
	if err := m.db.MarkFeudQuestionPlayed(q.ID, g.GameNo); err != nil {
		return err
	}
	g.startRound(q.ID, q.Text, slots, now, m.randN)
	return nil
}

// boardSlots are a question's top answers.
func (m *Module) boardSlots(qid int64) ([]Slot, error) {
	answers, err := m.db.FeudAnswers(qid)
	if err != nil {
		return nil, err
	}
	aliases, err := m.db.FeudAnswerAliases(qid)
	if err != nil {
		return nil, err
	}
	if len(answers) > boardSlots {
		answers = answers[:boardSlots]
	}
	slots := make([]Slot, len(answers))
	for n, a := range answers {
		slots[n] = Slot{AnswerID: a.ID, Label: a.Label, Points: a.Score(), Aliases: aliases[a.ID], By: -1}
	}
	return slots, nil
}

// judge checks a locked-in guess against the board and scores it. Exact
// matches are instant; otherwise the agent decides close calls.
func (m *Module) judge(guildID string, seq int, guess string) {
	g, err := m.loadGame(guildID)
	if err != nil || g == nil || g.TurnSeq != seq || g.Phase != phaseJudging {
		return
	}
	slot := g.matchExact(normalize(guess))
	if slot < 0 {
		slot = m.matchLLM(g, guess)
	}
	m.mutate(guildID, func(cur *Game) error {
		if cur.TurnSeq != seq || cur.Phase != phaseJudging {
			return errStale
		}
		cur.applyGuess(guess, slot, m.now())
		return nil
	}, func(_ *Game, err error) {
		if err != nil && !errors.Is(err, errStale) && !errors.Is(err, errNoGame) {
			m.config.Logger.Warnf("feud: judge: %v", err)
		}
	})
}

const judgeSystem = `You judge guesses in a Family Feud style game. Decide whether a guess means the same thing as one of the survey answers: a synonym, a misspelling, a more or less specific wording of the same idea, or a brand that is clearly the same thing. A related but different thing does not match. Reply with only the number of the matching answer, or 0 if none match.`

// matchLLM asks the agent which answer, if any, the guess means.
func (m *Module) matchLLM(g *Game, guess string) int {
	if m.agent == nil || len(g.Slots) == 0 {
		return -1
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Question: %s\nAnswers:\n", g.Question)
	for n, s := range g.Slots {
		fmt.Fprintf(&b, "%d. %s\n", n+1, s.Label)
	}
	fmt.Fprintf(&b, "Guess: %q", guess)
	ctx, cancel := context.WithTimeout(context.Background(), llmTimeout)
	defer cancel()
	reply, err := m.agent.Complete(ctx, judgeSystem, b.String(), nil)
	if err != nil {
		m.config.Logger.Warnf("feud: judge guess: %v", err)
		return -1
	}
	return parseSlot(reply, len(g.Slots))
}

// parseSlot reads the first number in the reply as a 1-based slot.
func parseSlot(reply string, n int) int {
	f := strings.FieldsFunc(reply, func(r rune) bool { return r < '0' || r > '9' })
	if len(f) == 0 {
		return -1
	}
	v, err := strconv.Atoi(f[0])
	if err != nil || v < 1 || v > n {
		return -1
	}
	return v - 1
}

// Recover re-arms timers for games that were running before a restart.
func (m *Module) Recover() {
	if m.db == nil {
		return
	}
	rows, err := m.db.LiveFeudGames()
	if err != nil {
		m.config.Logger.Warnf("feud: load live games: %v", err)
		return
	}
	for _, row := range rows {
		g, err := decodeGame(row)
		if err != nil {
			m.config.Logger.Warnf("feud: %v", err)
			continue
		}
		switch {
		case g.Phase == phaseJudging:
			// The check died with the old process; run it again with a
			// fresh expiry.
			if cur, _, err := m.change(g.GuildID, func(cur *Game) error {
				if cur.Phase != phaseJudging || cur.TurnSeq != g.TurnSeq {
					return errStale
				}
				cur.Deadline = m.now().Add(judgeWait)
				return nil
			}); err == nil {
				g = cur
				m.spawn(func() { m.judge(g.GuildID, g.TurnSeq, g.Locked) })
			}
		case g.Phase == phaseTurn && g.PanelMessage == "":
			// The restart came between saving the turn and posting its panel.
			g = m.repostPanel(g)
		}
		m.arm(g.GuildID, g.Deadline)
	}
}

// repostPanel posts a turn's missing panel and restarts the turn's clock, so
// the team isn't struck for a panel they never saw. It returns the newest
// state.
func (m *Module) repostPanel(g *Game) *Game {
	cur, _, err := m.change(g.GuildID, func(cur *Game) error {
		if cur.Phase != phaseTurn || cur.TurnSeq != g.TurnSeq || cur.PanelMessage != "" {
			return errStale
		}
		cur.Deadline = m.now().Add(cur.turnLength())
		return nil
	})
	if err != nil {
		return g
	}
	board, ok, err := m.db.FeudBoard(cur.GuildID)
	if err != nil || !ok {
		return cur
	}
	m.postPanel(cur, [2]string{board.RedThreadID, board.BlueThreadID}[cur.Turn])
	m.refreshBoard(board, cur)
	if latest, err := m.loadGame(cur.GuildID); err == nil && latest != nil {
		return latest
	}
	return cur
}

// sweep is the safety net for missed timers.
func (m *Module) sweep() error {
	rows, err := m.db.LiveFeudGames()
	if err != nil {
		return err
	}
	for _, row := range rows {
		g, err := decodeGame(row)
		if err != nil || g.Deadline.IsZero() || m.now().Before(g.Deadline.Add(5*time.Second)) {
			continue
		}
		m.tick(g.GuildID)
	}
	return nil
}

// ---- Interactions ----

func (m *Module) handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ch := m.config.ForGuild(i.GuildID).GetFeudChannelID()
	if ch == "" {
		m.respondText(s, i, "❌ Set the GP Feud channel in `/config` first.")
		return
	}
	// Held through the whole setup so a game can't start while the threads
	// it would use are being replaced.
	defer m.guildLock(i.GuildID)()
	if g, err := m.loadGame(i.GuildID); err != nil || g != nil {
		m.respondText(s, i, "❌ End the current game first.")
		return
	}
	m.respondText(s, i, "Setting up the board in <#"+ch+">…")
	api := m.dc(s)
	board := database.FeudBoard{GuildID: i.GuildID, ChannelID: ch}
	msg, err := api.Send(ch, &discordgo.MessageSend{Embeds: []*discordgo.MessageEmbed{boardEmbed(nil, board)}, Components: boardComponents()})
	if err != nil {
		m.config.Logger.Warnf("feud: post board: %v", err)
		return
	}
	board.MessageID = msg.ID
	for team, id := range []*string{&board.RedThreadID, &board.BlueThreadID} {
		th, err := api.CreateThread(ch, teamEmoji(team)+" "+teamNames[team]+" Team")
		if err != nil {
			m.config.Logger.Warnf("feud: create %s thread: %v", teamNames[team], err)
			return
		}
		*id = th.ID
	}
	if err := m.db.SaveFeudBoard(board); err != nil {
		m.config.Logger.Warnf("feud: save board: %v", err)
	}
}

func (m *Module) handleStart(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Held until the new game is saved, so a Join or End can't land between
	// creating the game and its first save and then be overwritten.
	defer m.guildLock(i.GuildID)()
	board, ok, err := m.db.FeudBoard(i.GuildID)
	if err != nil || !ok {
		m.respondText(s, i, "❌ Run `/feud-admin setup` first.")
		return
	}
	gc := m.config.ForGuild(i.GuildID)
	need := gc.GetFeudMinRespondents()
	st, err := m.db.FeudPoolStats(i.GuildID, need)
	if err != nil {
		m.respondText(s, i, "❌ Couldn't check the question pool.")
		return
	}
	if st.Ready == 0 {
		m.respondText(s, i, fmt.Sprintf("❌ No questions are ready yet. Each needs %d answers; see `/feud-admin pool`.", need))
		return
	}
	g := newGame(i.GuildID, userID(i), min(gc.GetFeudRounds(), st.Ready), gc.GetFeudTurnTimer())
	state, _ := json.Marshal(g)
	id, gameNo, err := m.db.CreateFeudGame(i.GuildID, string(state), m.now())
	if errors.Is(err, database.ErrFeudGameRunning) {
		m.respondText(s, i, "❌ A game is already running.")
		return
	}
	if err != nil {
		m.config.Logger.Warnf("feud: create game: %v", err)
		m.respondText(s, i, "❌ Couldn't start a game.")
		return
	}
	g.ID, g.GameNo = id, gameNo
	if err := m.saveGame(g); err != nil {
		m.config.Logger.Warnf("feud: save game: %v", err)
	}
	m.respondText(s, i, fmt.Sprintf("✅ Game on! Players can join from the board in <#%s>.", board.ChannelID))
	m.refreshBoard(board, g)
}

func (m *Module) handleEnd(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.mutate(i.GuildID, func(g *Game) error {
		g.finish(m.now(), "Game ended by a moderator.")
		return nil
	}, func(_ *Game, err error) {
		switch {
		case errors.Is(err, errNoGame):
			m.respondText(s, i, "No game is running.")
		case err != nil:
			m.config.Logger.Warnf("feud: end game: %v", err)
			m.respondText(s, i, "❌ Couldn't end the game.")
		default:
			m.respondText(s, i, "✅ Game ended.")
		}
	})
}

func (m *Module) handleJoin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	uid := userID(i)
	var team int
	m.mutate(i.GuildID, func(g *Game) error {
		var err error
		team, err = g.join(uid, m.now(), m.randN)
		return err
	}, func(_ *Game, err error) { m.joinReply(s, i, team, err) })
}

func (m *Module) joinReply(s *discordgo.Session, i *discordgo.InteractionCreate, team int, err error) {
	board, _, _ := m.db.FeudBoard(i.GuildID)
	threads := [2]string{board.RedThreadID, board.BlueThreadID}
	switch {
	case errors.Is(err, errNoGame), errors.Is(err, errGameOver):
		m.respondText(s, i, "No game is running right now. Answer some survey questions so the next one is ready!",
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{answerButton("Answer questions")}})
	case errors.Is(err, errAlreadyIn):
		m.respondText(s, i, fmt.Sprintf("You're already on %s %s: <#%s>", teamEmoji(team), teamNames[team], threads[team]))
	case errors.Is(err, errWaitRound):
		m.respondText(s, i, "You left during this round. You can rejoin when it ends.")
	case err != nil:
		m.config.Logger.Warnf("feud: join: %v", err)
		m.respondText(s, i, "❌ Couldn't join. Try again in a moment.")
	default:
		m.respondText(s, i, fmt.Sprintf("You're on %s **%s**! Head to <#%s> to plan with your team.", teamEmoji(team), teamNames[team], threads[team]))
	}
}

func (m *Module) handleLeave(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.leaveGame(i.GuildID, userID(i), func(msg string) { m.respondText(s, i, msg) })
}

func (m *Module) leaveGame(guildID, uid string, reply func(string)) {
	var locked string
	var seq int
	m.mutate(guildID, func(g *Game) error {
		var err error
		locked, err = g.leave(uid, m.now())
		seq = g.TurnSeq
		return err
	}, func(_ *Game, err error) {
		switch {
		case errors.Is(err, errNoGame), errors.Is(err, errNotInGame):
			reply("You're not in the game.")
		case err != nil:
			m.config.Logger.Warnf("feud: leave: %v", err)
			reply("❌ Couldn't leave. Try again in a moment.")
		default:
			reply("You left the game. You can rejoin after this round.")
		}
	})
	if locked != "" {
		m.spawn(func() { m.judge(guildID, seq, locked) })
	}
}

// OnThreadMembersUpdate treats leaving a team thread as leaving the game.
func (m *Module) OnThreadMembersUpdate(_ *discordgo.Session, u *discordgo.ThreadMembersUpdate) {
	if m.db == nil || len(u.RemovedMembers) == 0 {
		return
	}
	board, ok, err := m.db.FeudBoard(u.GuildID)
	if err != nil || !ok || (u.ID != board.RedThreadID && u.ID != board.BlueThreadID) {
		return
	}
	team := red
	if u.ID == board.BlueThreadID {
		team = blue
	}
	for _, uid := range u.RemovedMembers {
		var locked string
		var seq int
		m.mutate(u.GuildID, func(g *Game) error {
			// Only someone still on this team; removals the bot made are
			// already done.
			if p := g.player(uid); p == nil || !p.Active || p.Team != team {
				return errNotInGame
			}
			var err error
			locked, err = g.leave(uid, m.now())
			seq = g.TurnSeq
			return err
		}, func(_ *Game, err error) {
			if err != nil && !errors.Is(err, errNotInGame) && !errors.Is(err, errNoGame) {
				m.config.Logger.Warnf("feud: thread leave: %v", err)
			}
		})
		if locked != "" {
			m.spawn(func() { m.judge(u.GuildID, seq, locked) })
		}
	}
}

// turnError explains why a suggestion or vote was refused.
func turnError(err error) string {
	switch {
	case errors.Is(err, errNotYourTurn):
		return "It's the other team's turn."
	case errors.Is(err, errStale), errors.Is(err, errNoTurn), errors.Is(err, errNoGame):
		return "That turn is over."
	case errors.Is(err, errNotInGame):
		return "You're not playing. Join from the board first."
	case errors.Is(err, errTooMany):
		return "There are plenty of suggestions already. Vote for one!"
	case errors.Is(err, errNoSuggestion):
		return "That suggestion is gone."
	}
	return "❌ Something went wrong. Try again in a moment."
}

func (m *Module) openSuggestModal(s *discordgo.Session, i *discordgo.InteractionCreate, key string) {
	gameID, seq, ok := parseTurnKey(key)
	g, err := m.loadGame(i.GuildID)
	switch {
	case !ok:
		err = errStale
	case err == nil && g == nil:
		err = errNoGame
	case err == nil && g.ID != gameID:
		err = errStale
	case err == nil:
		err = g.canVote(userID(i), seq)
	}
	if err != nil {
		m.respondText(s, i, turnError(err))
		return
	}
	m.respond(s, i, discordgo.InteractionResponseModal, &discordgo.InteractionResponseData{
		CustomID: prefix + "sg:" + key,
		Title:    "Suggest an answer",
		Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: "### " + g.Question},
			discordgo.Label{Label: "Your answer", Description: "Your vote moves to it. Your team can vote it in.",
				Component: discordgo.TextInput{CustomID: suggestInputID, Style: discordgo.TextInputShort,
					Required: new(true), MinLength: 1, MaxLength: maxGuessLen}},
		},
	})
}

func (m *Module) handleSuggest(s *discordgo.Session, i *discordgo.InteractionCreate, key string) {
	gameID, seq, ok := parseTurnKey(key)
	if !ok {
		m.respondText(s, i, turnError(errStale))
		return
	}
	text := truncate(cleanText(modalValue(i, suggestInputID)), maxGuessLen)
	norm := normalize(text)
	if !hasWord(text) || norm == "" {
		m.respondText(s, i, "❌ That answer needs some letters or numbers.")
		return
	}
	var locked string
	m.mutate(i.GuildID, func(g *Game) error {
		if g.ID != gameID {
			return errStale
		}
		var err error
		locked, err = g.suggest(userID(i), text, norm, seq, m.now())
		if errors.Is(err, errDuplicateIdea) {
			return nil
		}
		return err
	}, func(_ *Game, err error) { m.afterVote(s, i, err) })
	if locked != "" {
		m.spawn(func() { m.judge(i.GuildID, seq, locked) })
	}
}

func (m *Module) handleVote(s *discordgo.Session, i *discordgo.InteractionCreate, key, idStr string) {
	gameID, seq, ok := parseTurnKey(key)
	id, idErr := strconv.Atoi(idStr)
	if !ok || idErr != nil {
		m.respondText(s, i, turnError(errStale))
		return
	}
	var locked string
	m.mutate(i.GuildID, func(g *Game) error {
		if g.ID != gameID {
			return errStale
		}
		var err error
		locked, err = g.vote(userID(i), id, seq, m.now())
		return err
	}, func(_ *Game, err error) { m.afterVote(s, i, err) })
	if locked != "" {
		m.spawn(func() { m.judge(i.GuildID, seq, locked) })
	}
}

// afterVote acknowledges the click. The panel itself is redrawn by sync
// from the newest state, so simultaneous votes can't overwrite each other.
func (m *Module) afterVote(s *discordgo.Session, i *discordgo.InteractionCreate, err error) {
	if err != nil {
		m.respondText(s, i, turnError(err))
		return
	}
	m.respond(s, i, discordgo.InteractionResponseDeferredMessageUpdate, nil)
}

func teamEmoji(team int) string {
	if team == blue {
		return "🔵"
	}
	return "🔴"
}
