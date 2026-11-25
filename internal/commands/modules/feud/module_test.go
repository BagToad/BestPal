package feud

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const guild = "g1"

type fakeAPI struct {
	responses []*discordgo.InteractionResponse
	sent      map[string][]*discordgo.MessageSend
	edits     []*discordgo.MessageEdit
}

func (f *fakeAPI) Respond(_ *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	f.responses = append(f.responses, r)
	return nil
}

func (f *fakeAPI) Send(ch string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	if f.sent == nil {
		f.sent = map[string][]*discordgo.MessageSend{}
	}
	f.sent[ch] = append(f.sent[ch], msg)
	return &discordgo.Message{ID: "msg" + strconv.Itoa(len(f.sent[ch])), ChannelID: ch}, nil
}

func (f *fakeAPI) Edit(msg *discordgo.MessageEdit) (*discordgo.Message, error) {
	f.edits = append(f.edits, msg)
	return &discordgo.Message{ID: msg.ID}, nil
}

func (f *fakeAPI) last(t *testing.T) *discordgo.InteractionResponse {
	t.Helper()
	require.NotEmpty(t, f.responses)
	return f.responses[len(f.responses)-1]
}

func newTestModule(t *testing.T, kv map[string]any) (*Module, *fakeAPI, *database.DB) {
	t.Helper()
	db, err := database.NewDB(filepath.Join(t.TempDir(), "test.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	base := map[string]any{
		"gamerpals_server_id":         guild,
		config.KeyFeudEnabled:         true,
		config.KeyFeudReviewChannelID: "review",
		config.KeyFeudMinRespondents:  2,
	}
	for k, v := range kv {
		base[k] = v
	}
	api := &fakeAPI{}
	m := &Module{config: config.NewMockConfig(base), db: db, api: api,
		now: func() time.Time { return time.Unix(1000, 0) }, randN: func(int) int { return 0 }}
	return m, api, db
}

func component(uid, customID string, perms int64) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionMessageComponent, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}, Permissions: perms},
		Data:   discordgo.MessageComponentInteractionData{CustomID: customID},
	}}
}

func modal(uid, customID, inputID, value string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}},
		Data: discordgo.ModalSubmitInteractionData{CustomID: customID, Components: []discordgo.MessageComponent{
			&discordgo.Label{Component: &discordgo.TextInput{CustomID: inputID, Value: value}},
		}},
	}}
}

func TestNormalize(t *testing.T) {
	for in, want := range map[string]string{
		"The Cats!":    "cat",
		"  dogs  ":     "dog",
		"Don't Panic":  "dont panic",
		"bus":          "bus",
		"Glass":        "glass",
		"cookies":      "cookie",
		"a":            "a",
		"My Mom":       "mom",
		"Pizza, Hut":   "pizza hut",
		"gas":          "gas",
		"octopus":      "octopus",
		"!!!":          "",
		"Video Games ": "video game",
	} {
		assert.Equal(t, want, normalize(in), in)
	}
}

func TestSeedQuestionsAreUnique(t *testing.T) {
	seen := map[string]string{}
	for _, q := range seedQuestions() {
		require.NotEmpty(t, q.Norm, q.Text)
		if prev, dup := seen[q.Norm]; dup {
			t.Errorf("%q and %q normalize the same", prev, q.Text)
		}
		seen[q.Norm] = q.Text
		assert.LessOrEqual(t, len([]rune(q.Text)), maxQuestionLen, q.Text)
	}
	assert.GreaterOrEqual(t, len(seen), 100)
}

func TestAnswerFlow(t *testing.T) {
	m, api, db := newTestModule(t, nil)
	m.HandleComponent(nil, component("u1", "gf:answer", 0))
	r := api.last(t)
	require.Equal(t, discordgo.InteractionResponseModal, r.Type)
	qid := r.Data.CustomID[len("gf:ans:"):]

	m.HandleModalSubmit(nil, modal("u1", "gf:ans:"+qid, answerInputID, "  The Dogs "))
	assert.Contains(t, api.last(t).Data.Content, "Answer saved")
	m.HandleModalSubmit(nil, modal("u1", "gf:ans:"+qid, answerInputID, "cats"))
	assert.Contains(t, api.last(t).Data.Content, "already answered")
	m.HandleModalSubmit(nil, modal("u2", "gf:ans:"+qid, answerInputID, "???"))
	assert.Contains(t, api.last(t).Data.Content, "letters, numbers or an emoji")
	m.HandleModalSubmit(nil, modal("u3", "gf:ans:"+qid, answerInputID, "😂"))
	assert.Contains(t, api.last(t).Data.Content, "Answer saved")
	m.HandleModalSubmit(nil, modal("u2", "gf:ans:"+qid, answerInputID, "dog"))

	id, _ := strconv.ParseInt(qid, 10, 64)
	answers, err := db.FeudAnswers(id)
	require.NoError(t, err)
	require.Len(t, answers, 2)
	assert.Equal(t, "The Dogs", answers[0].Label)
	assert.Equal(t, 2, answers[0].Score())

	// The next question for u3 skips the now-playable one in favour of one
	// still collecting answers.
	m.HandleComponent(nil, component("u3", "gf:answer", 0))
	assert.NotEqual(t, "gf:ans:"+qid, api.last(t).Data.CustomID)
}

func TestNextQuestionFillsClosestFirst(t *testing.T) {
	m, _, db := newTestModule(t, map[string]any{config.KeyFeudMinRespondents: 3})
	_, err := db.SeedFeudQuestions(guild, []database.FeudSeed{{Text: "Q1 question", Norm: "q1"}, {Text: "Q2 question", Norm: "q2"}, {Text: "Q3 question", Norm: "q3"}}, m.now())
	require.NoError(t, err)
	m.seeded.Store(guild, struct{}{})
	qs, err := db.FeudQuestionsToAnswer(guild, "x")
	require.NoError(t, err)
	byNorm := map[string]int64{}
	for _, q := range qs {
		byNorm[q.Text] = q.ID
	}
	for _, u := range []string{"a", "b", "c"} {
		_, err = db.AddFeudResponse(guild, byNorm["Q1 question"], u, "x", "x", m.now())
		require.NoError(t, err)
	}
	_, err = db.AddFeudResponse(guild, byNorm["Q2 question"], "a", "x", "x", m.now())
	require.NoError(t, err)

	q, ok, err := m.nextQuestion(guild, "z")
	require.NoError(t, err)
	require.True(t, ok)
	assert.Equal(t, "Q2 question", q.Text, "closest to ready but not ready yet")

	for _, u := range []string{"b", "c"} {
		_, err = db.AddFeudResponse(guild, byNorm["Q2 question"], u, "x", "x", m.now())
		require.NoError(t, err)
	}
	q, _, err = m.nextQuestion(guild, "z")
	require.NoError(t, err)
	assert.Equal(t, "Q3 question", q.Text)

	_, err = db.AddFeudResponse(guild, byNorm["Q3 question"], "z", "x", "x", m.now())
	require.NoError(t, err)
	q, ok, err = m.nextQuestion(guild, "z")
	require.NoError(t, err)
	require.True(t, ok, "falls back to playable questions")
	assert.Equal(t, 3, q.Respondents)
}

func TestDisabledFeud(t *testing.T) {
	m, api, _ := newTestModule(t, map[string]any{config.KeyFeudEnabled: false})
	m.HandleComponent(nil, component("u1", "gf:answer", 0))
	assert.Contains(t, api.last(t).Data.Content, "isn't running")
	m.HandleComponent(nil, component("u1", "gf:submit", 0))
	assert.Contains(t, api.last(t).Data.Content, "isn't running")
}

func TestSubmitAndReview(t *testing.T) {
	m, api, db := newTestModule(t, nil)
	m.HandleModalSubmit(nil, modal("u1", "gf:sub", questionInputID, "Name a thing you lose often."))
	assert.Contains(t, api.last(t).Data.Content, "Mods will review")
	require.Len(t, api.sent["review"], 1)
	assert.Equal(t, "Name a thing you lose often.", api.sent["review"][0].Embeds[0].Description)

	m.HandleModalSubmit(nil, modal("u2", "gf:sub", questionInputID, "name a THING you lose often"))
	assert.Contains(t, api.last(t).Data.Content, "already asked")
	m.HandleModalSubmit(nil, modal("u2", "gf:sub", questionInputID, "hi"))
	assert.Contains(t, api.last(t).Data.Content, "at least")
	for n := 0; n < maxPendingByUser-1; n++ {
		m.HandleModalSubmit(nil, modal("u1", "gf:sub", questionInputID, "Name a fun thing number "+strconv.Itoa(n)))
	}
	m.HandleModalSubmit(nil, modal("u1", "gf:sub", questionInputID, "Name one more thing please"))
	assert.Contains(t, api.last(t).Data.Content, "waiting for review")

	q, ok, err := db.OldestPendingFeudQuestion(guild)
	require.NoError(t, err)
	require.True(t, ok)
	id := strconv.FormatInt(q.ID, 10)

	m.HandleComponent(nil, component("u2", "gf:approve:"+id, 0))
	assert.Contains(t, api.last(t).Data.Content, "Only moderators")

	m.HandleComponent(nil, component("mod", "gf:approve:"+id, discordgo.PermissionBanMembers))
	r := api.last(t)
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, r.Type)
	assert.Contains(t, r.Data.Embeds[0].Fields[1].Value, "Approved")

	// The review queue flow moves on and syncs the channel post.
	next, ok, err := db.OldestPendingFeudQuestion(guild)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, db.SetFeudQuestionReviewMessage(next.ID, "review", "post1"))
	m.HandleComponent(nil, component("mod", "gf:reject:"+strconv.FormatInt(next.ID, 10)+":next", discordgo.PermissionAdministrator))
	r = api.last(t)
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, r.Type)
	require.Len(t, api.edits, 1)
	assert.Equal(t, "post1", api.edits[0].ID)
	assert.Contains(t, (*api.edits[0].Embeds)[0].Fields[1].Value, "Rejected")

	m.HandleComponent(nil, component("mod", "gf:reject:"+id, discordgo.PermissionBanMembers))
	r = api.last(t)
	assert.Contains(t, (r.Data.Embeds[0].Fields[1].Value), "Approved", "the first decision stands")

	got, _, err := db.FeudQuestion(guild, q.ID)
	require.NoError(t, err)
	assert.Equal(t, database.FeudActive, got.Status)
}
