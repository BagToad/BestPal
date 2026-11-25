package feud

import (
	"context"
	"sort"
	"strings"
	"testing"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeAgent struct {
	reply   string
	prompts []string
}

func (f *fakeAgent) Complete(_ context.Context, _, prompt string, _ []copilot.Tool) (string, error) {
	f.prompts = append(f.prompts, prompt)
	return f.reply, nil
}

func adminCmd(uid, sub string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionApplicationCommand, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}, Permissions: discordgo.PermissionBanMembers},
		Data: discordgo.ApplicationCommandInteractionData{Name: "feud-admin", Options: []*discordgo.ApplicationCommandInteractionDataOption{
			{Name: sub, Type: discordgo.ApplicationCommandOptionSubCommand}}},
	}}
}

// readyQuestion seeds a question with enough answers to play.
func readyQuestion(t *testing.T, db *database.DB, text string, answers map[string]string) {
	t.Helper()
	_, err := db.SubmitFeudQuestion(guild, "u", text, normalize(text), 3, time.Unix(1, 0))
	require.NoError(t, err)
	q, ok, err := db.OldestPendingFeudQuestion(guild)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = db.ReviewFeudQuestion(guild, q.ID, true, "mod", time.Unix(1, 0))
	require.NoError(t, err)
	uids := make([]string, 0, len(answers))
	for uid := range answers {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	for _, uid := range uids {
		a := answers[uid]
		_, err := db.AddFeudResponse(guild, q.ID, uid, a, normalize(a), time.Unix(1, 0))
		require.NoError(t, err)
	}
}

func liveGame(t *testing.T, m *Module) *Game {
	t.Helper()
	g, err := m.loadGame(guild)
	require.NoError(t, err)
	require.NotNil(t, g)
	return g
}

func TestGameFlow(t *testing.T) {
	m, api, db := newTestModule(t, map[string]any{config.KeyFeudChannelID: "feudch", config.KeyFeudRounds: 1})
	m.seeded.Store(guild, struct{}{})
	now := time.Unix(5000, 0)
	m.now = func() time.Time { return now }
	agent := &fakeAgent{reply: "2"}
	m.SetAgent(agent)

	m.handleAdmin(nil, adminCmd("mod", "start"))
	assert.Contains(t, api.last(t).Data.Content, "setup")

	m.handleAdmin(nil, adminCmd("mod", "setup"))
	board, ok, err := db.FeudBoard(guild)
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, database.FeudBoard{GuildID: guild, ChannelID: "feudch", MessageID: "msg1", RedThreadID: "thread1", BlueThreadID: "thread2"}, board)

	m.handleAdmin(nil, adminCmd("mod", "start"))
	assert.Contains(t, api.last(t).Data.Content, "No questions are ready")

	readyQuestion(t, db, "Name a pet", map[string]string{"r1": "Dogs", "r2": "dog", "r3": "Cat", "r4": "cats"})
	m.handleAdmin(nil, adminCmd("mod", "start"))
	assert.Contains(t, api.last(t).Data.Content, "Game on")
	m.handleAdmin(nil, adminCmd("mod", "start"))
	assert.Contains(t, api.last(t).Data.Content, "already running")

	m.HandleComponent(nil, component("a", "gf:join", 0))
	assert.Contains(t, api.last(t).Data.Content, "Red")
	m.HandleComponent(nil, component("x", "gf:join", 0))
	assert.Contains(t, api.last(t).Data.Content, "Blue")
	assert.True(t, api.members["thread1"]["a"])
	assert.True(t, api.members["thread2"]["x"])
	welcome := api.sent["thread1"][0]
	assert.Contains(t, welcome.Content, "<@a>")
	assert.Equal(t, []string{"a"}, welcome.AllowedMentions.Users)

	g := liveGame(t, m)
	require.Equal(t, phaseLobby, g.Phase)
	require.True(t, now.Add(lobbyWait).Equal(g.Deadline))

	m.tick(guild)
	assert.Equal(t, phaseLobby, liveGame(t, m).Phase, "too early")
	now = now.Add(lobbyWait)
	m.tick(guild)
	g = liveGame(t, m)
	require.Equal(t, phaseTurn, g.Phase)
	require.Equal(t, red, g.Turn)
	require.Len(t, g.Slots, 2)
	assert.Equal(t, "Dogs", g.Slots[0].Label)
	assert.Equal(t, 2, g.Slots[0].Points)
	require.NotEmpty(t, g.PanelMessage, "the turn panel was posted and saved")
	panel := api.sent["thread1"][len(api.sent["thread1"])-1]
	assert.Contains(t, panel.Content, "your turn")
	q, _, _ := db.FeudQuestion(guild, g.QuestionID)
	assert.Equal(t, 1, q.TimesPlayed)

	seq := turnKey(g)
	m.HandleComponent(nil, component("x", "gf:suggest:"+seq, 0))
	assert.Contains(t, api.last(t).Data.Content, "other team's turn")
	m.HandleComponent(nil, component("a", "gf:suggest:"+seq, 0))
	assert.Equal(t, discordgo.InteractionResponseModal, api.last(t).Type)

	// A solo player's suggestion locks in; "doggo" isn't an alias, so the
	// agent judges it.
	agent.reply = "1"
	m.HandleModalSubmit(nil, modal("a", "gf:sg:"+seq, suggestInputID, "doggo"))
	assert.Equal(t, discordgo.InteractionResponseDeferredMessageUpdate, api.last(t).Type)
	var lockedNote bool
	for _, e := range api.edits {
		lockedNote = lockedNote || (e.Content != nil && strings.Contains(*e.Content, "locked in"))
	}
	assert.True(t, lockedNote, "the panel shows the locked guess")
	require.Len(t, agent.prompts, 1)
	assert.Contains(t, agent.prompts[0], "1. Dogs\n2. Cat")
	g = liveGame(t, m)
	assert.Equal(t, 2, g.Score[red])
	assert.Equal(t, blue, g.Turn)
	assert.True(t, g.Slots[0].Shown)

	// Blue's "The cats" matches exactly, so no agent call.
	seq = turnKey(g)
	m.HandleModalSubmit(nil, modal("x", "gf:sg:"+seq, suggestInputID, "The cats"))
	assert.Len(t, agent.prompts, 1)
	row, ok, err := db.LastFeudGame(guild)
	require.NoError(t, err)
	require.True(t, ok)
	assert.True(t, row.Ended)
	g, err = decodeGame(row)
	require.NoError(t, err)
	assert.Equal(t, phaseEnded, g.Phase, "board cleared in the only round")
	assert.Equal(t, [2]int{2, 2}, g.Score)
	assert.Equal(t, "It's a tie!", g.Result)
	assert.Empty(t, api.members["thread1"], "players leave the threads at the end")
	assert.Empty(t, api.members["thread2"])
	last := api.edits[len(api.edits)-1]
	assert.Equal(t, "msg1", last.ID)
	assert.Contains(t, (*last.Embeds)[0].Title, "Game over")

	m.HandleComponent(nil, component("a", "gf:join", 0))
	assert.Contains(t, api.last(t).Data.Content, "No game is running")
}

func TestThreadLeavePausesAndModEnds(t *testing.T) {
	m, api, db := newTestModule(t, map[string]any{config.KeyFeudChannelID: "feudch"})
	m.seeded.Store(guild, struct{}{})
	now := time.Unix(5000, 0)
	m.now = func() time.Time { return now }
	readyQuestion(t, db, "Name a pet", map[string]string{"r1": "Dog", "r2": "Cat"})
	readyQuestion(t, db, "Name a fruit", map[string]string{"r1": "Apple", "r2": "Pear"})
	m.handleAdmin(nil, adminCmd("mod", "setup"))
	m.handleAdmin(nil, adminCmd("mod", "start"))
	assert.Equal(t, 2, liveGame(t, m).Rounds, "rounds are capped by ready questions")
	m.HandleComponent(nil, component("a", "gf:join", 0))
	m.HandleComponent(nil, component("x", "gf:join", 0))
	now = now.Add(lobbyWait)
	m.tick(guild)

	// Someone outside the game leaving the thread changes nothing.
	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: "thread2", GuildID: guild, RemovedMembers: []string{"zz"}})
	assert.Equal(t, phaseTurn, liveGame(t, m).Phase)

	m.OnThreadMembersUpdate(nil, &discordgo.ThreadMembersUpdate{ID: "thread2", GuildID: guild, RemovedMembers: []string{"x"}})
	g := liveGame(t, m)
	assert.Equal(t, phasePaused, g.Phase)
	m.HandleComponent(nil, component("x", "gf:join", 0))
	assert.Contains(t, api.last(t).Data.Content, "rejoin when it ends")

	m.HandleComponent(nil, component("a", "gf:leave", 0))
	assert.Contains(t, api.last(t).Data.Content, "You left")
	assert.False(t, api.members["thread1"]["a"])

	m.handleAdmin(nil, adminCmd("mod", "end"))
	assert.Contains(t, api.last(t).Data.Content, "Game ended")
	_, live, err := db.LiveFeudGame(guild)
	require.NoError(t, err)
	assert.False(t, live)
	var over bool
	for _, msg := range api.sent["thread1"] {
		over = over || strings.Contains(msg.Content, "Game ended by a moderator")
	}
	assert.True(t, over)
}

func TestPauseTimesOut(t *testing.T) {
	m, _, db := newTestModule(t, map[string]any{config.KeyFeudChannelID: "feudch"})
	m.seeded.Store(guild, struct{}{})
	now := time.Unix(5000, 0)
	m.now = func() time.Time { return now }
	readyQuestion(t, db, "Name a pet", map[string]string{"r1": "Dog", "r2": "Cat"})
	m.handleAdmin(nil, adminCmd("mod", "setup"))
	m.handleAdmin(nil, adminCmd("mod", "start"))
	m.HandleComponent(nil, component("a", "gf:join", 0))
	m.HandleComponent(nil, component("x", "gf:join", 0))
	now = now.Add(lobbyWait)
	m.tick(guild)
	m.HandleComponent(nil, component("x", "gf:leave", 0))
	now = now.Add(pauseWait)
	require.NoError(t, m.sweep(), "the sweep waits a few seconds past the deadline")
	assert.Equal(t, phasePaused, liveGame(t, m).Phase)
	now = now.Add(10 * time.Second)
	require.NoError(t, m.sweep())
	_, live, err := db.LiveFeudGame(guild)
	require.NoError(t, err)
	assert.False(t, live)
}

func TestTimerTurnoverAndRecovery(t *testing.T) {
	m, api, db := newTestModule(t, map[string]any{config.KeyFeudChannelID: "feudch", config.KeyFeudTurnTimer: "30s"})
	m.seeded.Store(guild, struct{}{})
	now := time.Unix(5000, 0)
	m.now = func() time.Time { return now }
	var armed []time.Duration
	m.afterFunc = func(d time.Duration, _ func()) func() { armed = append(armed, d); return func() {} }
	readyQuestion(t, db, "Name a pet", map[string]string{"r1": "Dog", "r2": "Cat"})
	m.handleAdmin(nil, adminCmd("mod", "setup"))
	m.handleAdmin(nil, adminCmd("mod", "start"))
	m.HandleComponent(nil, component("a", "gf:join", 0))
	m.HandleComponent(nil, component("x", "gf:join", 0))
	m.HandleComponent(nil, component("b", "gf:join", 0))
	now = now.Add(lobbyWait)
	m.tick(guild)
	g := liveGame(t, m)
	require.True(t, now.Add(30*time.Second).Equal(g.Deadline))
	assert.Equal(t, 30*time.Second+100*time.Millisecond, armed[len(armed)-1])
	seq := turnKey(g)
	panelID := g.PanelMessage

	// Red's two players split; time runs out and the earlier pick goes in.
	m.HandleModalSubmit(nil, modal("a", "gf:sg:"+seq, suggestInputID, "Hamster"))
	m.HandleModalSubmit(nil, modal("b", "gf:sg:"+seq, suggestInputID, "Cat"))
	g = liveGame(t, m)
	require.Equal(t, phaseTurn, g.Phase, "1 of 2 isn't a majority")
	var comps []discordgo.MessageComponent
	for _, e := range api.edits {
		if e.ID == panelID && e.Components != nil {
			comps = *e.Components
		}
	}
	require.Len(t, comps, 2, "suggest row and a row of vote buttons")

	// A restart while the turn runs keeps the deadline.
	m2 := New(&types.Dependencies{Config: m.config, DB: m.db})
	m2.api, m2.now, m2.spawn = api, m.now, m.spawn
	var rearmed time.Duration
	m2.afterFunc = func(d time.Duration, _ func()) func() { rearmed = d; return func() {} }
	m2.Recover()
	assert.Equal(t, 30*time.Second+100*time.Millisecond, rearmed)

	now = now.Add(30 * time.Second)
	m.tick(guild)
	g = liveGame(t, m)
	assert.Equal(t, 1, g.Strikes[red], "Hamster isn't on the board")
	assert.Equal(t, blue, g.Turn)
	var closed bool
	for _, e := range api.edits {
		closed = closed || (e.ID == panelID && strings.Contains(*e.Content, "Hamster"))
	}
	assert.True(t, closed, "the old panel shows what happened")
}

func TestStaleJoinDoesNotRestoreLeaver(t *testing.T) {
	m, api, _ := newTestModule(t, map[string]any{config.KeyFeudChannelID: "feudch"})
	threads := [2]string{"thread1", "thread2"}
	empty := &Game{GuildID: guild, Phase: phaseLobby}
	joined := &Game{GuildID: guild, Phase: phaseLobby, Players: []Player{{UserID: "a", Team: red, Active: true}}}
	left := &Game{GuildID: guild, Phase: phaseLobby, Players: []Player{{UserID: "a", Team: red}}}

	// The Join's side effects arrive after the Leave has been saved.
	m.syncThreads(empty, joined, left, threads)
	assert.False(t, api.members["thread1"]["a"])
	assert.Empty(t, api.sent["thread1"], "no welcome for someone who already left")
}

func TestGameOverBoardKeepsAnswers(t *testing.T) {
	g := &Game{Phase: phaseEnded, Result: "It's a tie!", Question: "Name a pet", Slots: testSlots("Dog", "Cat")}
	for n := range g.Slots {
		g.Slots[n].Shown = true
	}
	e := boardEmbed(g, database.FeudBoard{})
	assert.Contains(t, e.Description, "Name a pet")
	require.NotEmpty(t, e.Fields)
	assert.Contains(t, e.Fields[0].Value, "Dog")
}
