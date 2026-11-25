package feud

import (
	"context"
	"strconv"
	"testing"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
	copilot "github.com/github/copilot-sdk/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// toolAgent plays the agent by calling the given tools.
type toolAgent struct {
	system, prompt string
	run            func(tools map[string]copilot.Tool)
}

func (a *toolAgent) Complete(_ context.Context, system, prompt string, tools []copilot.Tool) (string, error) {
	a.system, a.prompt = system, prompt
	byName := map[string]copilot.Tool{}
	for _, t := range tools {
		byName[t.Name] = t
	}
	a.run(byName)
	return "done", nil
}

func call(t *testing.T, tools map[string]copilot.Tool, name string, args map[string]any) {
	t.Helper()
	_, err := tools[name].Handler(copilot.ToolInvocation{Arguments: args})
	require.NoError(t, err)
}

func selectValue(uid, customID string, perms int64, value string) *discordgo.InteractionCreate {
	i := component(uid, customID, perms)
	i.Message = &discordgo.Message{}
	i.Data = discordgo.MessageComponentInteractionData{CustomID: customID, Values: []string{value}}
	return i
}

func button1(uid, customID string, perms int64) *discordgo.InteractionCreate {
	i := component(uid, customID, perms)
	i.Message = &discordgo.Message{}
	return i
}

const mod = int64(discordgo.PermissionBanMembers)

// modModal is a modal submitted by a moderator from an editor message.
func modModal(customID, value string) *discordgo.InteractionCreate {
	i := modal("mod", customID, "v", value)
	i.Member.Permissions = mod
	i.Message = &discordgo.Message{}
	return i
}

func answersByLabel(t *testing.T, db *database.DB, qid int64) map[string]database.FeudAnswer {
	t.Helper()
	all, err := db.AllFeudAnswers(qid)
	require.NoError(t, err)
	out := map[string]database.FeudAnswer{}
	for _, a := range all {
		out[a.Label] = a
	}
	return out
}

func firstQuestionID(t *testing.T, db *database.DB, text string) int64 {
	t.Helper()
	q, _, err := db.FeudQuestionsPage(guild, 0, 50)
	require.NoError(t, err)
	for _, x := range q {
		if x.Text == text {
			return x.ID
		}
	}
	t.Fatalf("question %q not found", text)
	return 0
}

func TestEditorRequiresMod(t *testing.T) {
	m, api, _ := newTestModule(t, nil)
	m.HandleComponent(nil, button1("u", "gf:ed:list:0", 0))
	assert.Contains(t, api.last(t).Data.Content, "Only moderators")
	m.HandleModalSubmit(nil, modal("u", "gf:edq:1", "v", "Name something new"))
	assert.Contains(t, api.last(t).Data.Content, "Only moderators")
}

func TestEditorFlow(t *testing.T) {
	m, api, db := newTestModule(t, nil)
	m.seeded.Store(guild, struct{}{})
	readyQuestion(t, db, "Name a pet people keep.", map[string]string{"a": "Dog", "b": "Doggo", "c": "Cat", "d": "asdf"})
	qid := firstQuestionID(t, db, "Name a pet people keep.")
	qs := strconv.FormatInt(qid, 10)

	m.handleAdmin(nil, adminCmd("mod", "edit"))
	r := api.last(t)
	assert.Equal(t, discordgo.InteractionResponseChannelMessageWithSource, r.Type)
	assert.NotEmpty(t, r.Data.Components)

	m.HandleComponent(nil, selectValue("mod", "gf:ed:pick", mod, qs))
	r = api.last(t)
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, r.Type)
	assert.Contains(t, r.Data.Embeds[0].Description, "Doggo")

	// Merge Doggo into Dog.
	ans := answersByLabel(t, db, qid)
	dog, doggo := strconv.FormatInt(ans["Dog"].ID, 10), strconv.FormatInt(ans["Doggo"].ID, 10)
	m.HandleComponent(nil, selectValue("mod", "gf:ed:ans:"+qs, mod, doggo))
	assert.Contains(t, api.last(t).Data.Embeds[0].Description, "Doggo")
	m.HandleComponent(nil, button1("mod", "gf:ed:mg:"+doggo, mod))
	assert.Contains(t, api.last(t).Data.Content, "Merge **Doggo**")
	m.HandleComponent(nil, selectValue("mod", "gf:ed:mgto:"+doggo, mod, dog))
	assert.Contains(t, api.last(t).Data.Content, "Merged")
	assert.Equal(t, 2, answersByLabel(t, db, qid)["Dog"].Count)

	// Hide junk, rename, bonus.
	junk := strconv.FormatInt(ans["asdf"].ID, 10)
	m.HandleComponent(nil, button1("mod", "gf:ed:hd:"+junk, mod))
	assert.True(t, answersByLabel(t, db, qid)["asdf"].Hidden)
	m.HandleModalSubmit(nil, modModal("gf:edr:"+dog, "Dogs"))
	assert.Contains(t, api.last(t).Data.Content, "Renamed")
	m.HandleModalSubmit(nil, modModal("gf:edb:"+dog, "x"))
	assert.Contains(t, api.last(t).Data.Content, "whole number")
	m.HandleModalSubmit(nil, modModal("gf:edb:"+dog, "3"))
	assert.Equal(t, 5, answersByLabel(t, db, qid)["Dogs"].Score())

	// Add an answer through the modal; the form needs two fields.
	m.HandleComponent(nil, button1("mod", "gf:ed:aa:"+qs, mod))
	assert.Equal(t, discordgo.InteractionResponseModal, api.last(t).Type)
	assert.Len(t, api.last(t).Data.Components, 2)

	// Edit and retire the question.
	m.HandleModalSubmit(nil, modModal("gf:edq:"+qs, "Name pets"))
	assert.Contains(t, api.last(t).Data.Content, "at least")
	m.HandleModalSubmit(nil, modModal("gf:edq:"+qs, "Name a pet you would keep."))
	assert.Contains(t, api.last(t).Data.Content, "updated")
	m.HandleComponent(nil, button1("mod", "gf:ed:ret:"+qs, mod))
	assert.Contains(t, api.last(t).Data.Content, "retired")
	q, _, _ := db.FeudQuestion(guild, qid)
	assert.Equal(t, database.FeudRejected, q.Status)
	m.HandleComponent(nil, button1("mod", "gf:ed:ret:"+qs, mod))
	q, _, _ = db.FeudQuestion(guild, qid)
	assert.Equal(t, database.FeudActive, q.Status)
}

func TestEditorModalAddAnswer(t *testing.T) {
	m, api, db := newTestModule(t, nil)
	m.seeded.Store(guild, struct{}{})
	readyQuestion(t, db, "Name a pet people keep.", map[string]string{"a": "Dog"})
	qid := firstQuestionID(t, db, "Name a pet people keep.")
	qs := strconv.FormatInt(qid, 10)

	i := modal("mod", "gf:eda:"+qs, "v", "Fish")
	i.Member.Permissions = mod
	data := i.ModalSubmitData()
	data.Components = append(data.Components, &discordgo.Label{Component: &discordgo.TextInput{CustomID: "p", Value: "4"}})
	i.Data = data
	m.HandleModalSubmit(nil, i)
	assert.Contains(t, api.last(t).Data.Content, "Answer added")
	assert.Equal(t, 4, answersByLabel(t, db, qid)["Fish"].Score())
}

func TestDedupeTools(t *testing.T) {
	m, api, db := newTestModule(t, nil)
	m.seeded.Store(guild, struct{}{})
	readyQuestion(t, db, "Name a pet people keep.", map[string]string{"a": "Dog", "b": "Doggo", "c": "Cat", "d": "asdf"})
	qid := firstQuestionID(t, db, "Name a pet people keep.")
	ans := answersByLabel(t, db, qid)
	id := func(l string) float64 { return float64(ans[l].ID) }

	agent := &toolAgent{run: func(tools map[string]copilot.Tool) {
		call(t, tools, "merge_answers", map[string]any{"into_id": id("Dog"), "from_id": id("Doggo")})
		call(t, tools, "merge_answers", map[string]any{"into_id": id("Dog"), "from_id": id("Doggo")}) // already gone
		call(t, tools, "merge_answers", map[string]any{"into_id": id("Dog"), "from_id": 9999})
		call(t, tools, "hide_answer", map[string]any{"id": id("asdf"), "reason": "gibberish"})
		call(t, tools, "rename_answer", map[string]any{"id": id("Cat"), "label": "Cats"})
		call(t, tools, "rename_answer", map[string]any{"id": id("Cat"), "label": "Dog"}) // taken
	}}
	m.SetAgent(agent)
	actions, err := m.dedupeQuestion(context.Background(), guild, qid)
	require.NoError(t, err)
	assert.Equal(t, []string{"merged “Doggo” into “Dog”", "hid “asdf” (gibberish)", "renamed “Cat” to “Cats”"}, actions)
	assert.Contains(t, agent.prompt, "Doggo")

	after := answersByLabel(t, db, qid)
	assert.Equal(t, 2, after["Dog"].Count)
	assert.True(t, after["asdf"].Hidden)
	assert.Contains(t, after, "Cats")

	// Via the button: the agent edits the message through the webhook.
	agent.run = func(map[string]copilot.Tool) {}
	m.HandleComponent(nil, button1("mod", "gf:ed:dd:"+strconv.FormatInt(qid, 10), mod))
	assert.Equal(t, discordgo.InteractionResponseDeferredMessageUpdate, api.last(t).Type)
	require.Len(t, api.webhookEdits, 1)
	assert.Contains(t, *api.webhookEdits[0].Content, "Nothing needed")
}

func TestDedupeNeedsAgent(t *testing.T) {
	m, api, _ := newTestModule(t, nil)
	m.handleAdmin(nil, adminCmd("mod", "dedupe"))
	assert.Contains(t, api.last(t).Data.Content, "agent isn't running")
}

func TestDedupeGuildSkipsUnreadyQuestions(t *testing.T) {
	m, api, db := newTestModule(t, nil) // two answers needed
	m.seeded.Store(guild, struct{}{})
	readyQuestion(t, db, "Name a pet people keep.", map[string]string{"a": "Dog", "b": "Cat"})
	readyQuestion(t, db, "Name a drink people like.", map[string]string{"a": "Tea"})
	var asked int
	m.SetAgent(&toolAgent{run: func(map[string]copilot.Tool) { asked++ }})
	m.handleAdmin(nil, adminCmd("mod", "dedupe"))
	assert.Equal(t, discordgo.InteractionResponseDeferredChannelMessageWithSource, api.last(t).Type)
	assert.Equal(t, 1, asked)
	require.Len(t, api.webhookEdits, 1)
	assert.Contains(t, *api.webhookEdits[0].Content, "Checked 1 ready")
}
