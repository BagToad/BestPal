package squads

import (
	"encoding/json"
	"fmt"
	"testing"

	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/discordcompat"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func surveySubmit(uid string, squadID int64, reason, feedback string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}},
		Data: discordgo.ModalSubmitInteractionData{CustomID: fmt.Sprintf("%ssurvey:%d", prefix, squadID), Components: []discordgo.MessageComponent{
			&discordgo.TextDisplay{},
			&discordgo.Label{Component: &discordgo.TextInput{CustomID: surveyReasonID, Value: reason}},
			&discordgo.Label{Component: &discordgo.TextInput{CustomID: surveyFeedbackID, Value: feedback}},
			&discordgo.TextDisplay{},
		}},
	}}
}

func TestLeaveButtonOpensExitSurvey(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleave:%d", prefix, sq.ID)))
	r := api.lastResponse()
	require.Equal(t, discordgo.InteractionResponseModal, r.Type)
	assert.Equal(t, fmt.Sprintf("%ssurvey:%d", prefix, sq.ID), r.Data.CustomID)
	assert.LessOrEqual(t, len(r.Data.Title), 45)
	require.Len(t, r.Data.Components, 4)

	radio := r.Data.Components[1].(discordgo.Label).Component.(discordcompat.RadioGroup)
	assert.False(t, *radio.Required, "picking a reason is optional")
	var labels []string
	for _, o := range radio.Options {
		labels = append(labels, o.Label)
	}
	assert.Equal(t, []string{"This isn't a good match", "Nobody is responding", "Other"}, labels)

	text := r.Data.Components[2].(discordgo.Label).Component.(discordgo.TextInput)
	assert.Equal(t, discordgo.TextInputParagraph, text.Style)
	assert.False(t, *text.Required, "the text area is optional and always shown")
	assert.Contains(t, r.Data.Components[3].(discordgo.TextDisplay).Content, "This information is shared privately with app developers")

	b, err := json.Marshal(r)
	require.NoError(t, err)
	assert.Contains(t, string(b), `"type":21`)

	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad, "opening the survey doesn't leave yet")
}

func TestSquadLeaveCommandOpensExitSurvey(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	makeSquad(t, m, api, db, "a", "b", "c")
	m.handleSquad(nil, command("a", 0, "squad", "leave"))
	assert.Equal(t, discordgo.InteractionResponseModal, api.lastResponse().Type)
}

func TestExitSurveySubmitLeavesAndRecords(t *testing.T) {
	m, api, db, _ := newTestModule(t, map[string]any{config.KeySquadsFeedbackChannelID: "feedback", config.KeySquadsPlatformPCRoleID: "role-pc"})
	sq := makeSquad(t, m, api, db, "a", "b", "c", "d")
	member(api, "a", "role-eu", "role-18", "role-pc")
	member(api, "b", "role-eu", "role-25", "role-pc")
	member(api, "c", "role-na", "role-18")
	delete(api.members, "d") // left the server
	pick := func(uid string, ids ...int64) {
		for _, id := range ids {
			_, _, err := db.AddSquadPick(guild, uid, database.SquadPick{GameID: id, Name: fmt.Sprintf("Game %d", id)}, 25, t0)
			require.NoError(t, err)
		}
	}
	pick("a", 1, 2, 3, 4, 5)
	pick("b", 1, 2, 3, 4, 5)
	pick("c", 9)

	m.HandleModalSubmit(nil, surveySubmit("a", sq.ID, "no_response", "  nobody ever talked  "))
	assert.Contains(t, editContent(api.lastEdit()), "You left **Squad Axolotl**")
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.False(t, inSquad)

	got, err := db.SquadExitSurveys(guild)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].UserID)
	assert.Equal(t, sq.ID, got[0].SquadID)
	assert.Equal(t, "Squad Axolotl", got[0].SquadName)
	assert.Equal(t, "no_response", got[0].Reason)
	assert.Equal(t, "nobody ever talked", got[0].Feedback)

	var post *discordgo.MessageSend
	for _, s := range api.sent {
		if s.ChannelID == "feedback" {
			post = s.Msg
		}
	}
	require.NotNil(t, post, "posted to the feedback channel")
	assert.NotNil(t, post.AllowedMentions, "doesn't ping")
	e := post.Embeds[0]
	assert.Contains(t, e.Description, "## <@a>", "includes the user status")
	assert.Contains(t, e.Description, "**Compatibility:**")
	assert.Contains(t, e.Description, "**Region:** Europe")
	fields := map[string]string{}
	for _, f := range e.Fields {
		fields[f.Name] = f.Value
	}
	assert.Equal(t, "<@a> `a`", fields["Member"])
	assert.Contains(t, fields["Squad"], fmt.Sprintf("ID `%d`", sq.ID))
	assert.Contains(t, fields["Time in squad"], "👻 never spoke")
	assert.Equal(t, "Nobody is responding", fields["Reason"])
	assert.Equal(t, "nobody ever talked", fields["Feedback"])
	mates := fields["Squadmates when they left (3)"]
	assert.Equal(t, "• <@b> `b` — 🎮 Game 1, Game 2, Game 3 +2 · 🌍 same region · 🎂 age group 2 · 🕹️ PC\n"+
		"• <@c> `c` — 🎮 no shared games · 🌍 North America · 🎂 same age group · 🕹️ no shared platform\n"+
		"• <@d> `d` — not in the server", mates)
}

func TestExitSurveyBlankJustLeaves(t *testing.T) {
	m, api, db, _ := newTestModule(t, map[string]any{config.KeySquadsFeedbackChannelID: "feedback"})
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	m.HandleModalSubmit(nil, surveySubmit("a", sq.ID, "", " "))
	assert.Contains(t, editContent(api.lastEdit()), "You left **Squad Axolotl**")
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.False(t, inSquad)
	got, err := db.SquadExitSurveys(guild)
	require.NoError(t, err)
	assert.Empty(t, got)
	for _, s := range api.sent {
		assert.NotEqual(t, "feedback", s.ChannelID)
	}
}

func TestExitSurveyWithoutChannelStillSaves(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	m.HandleModalSubmit(nil, surveySubmit("a", sq.ID, "bogus", "hi"))
	got, err := db.SquadExitSurveys(guild)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Reason, "unknown reasons are dropped")
	assert.Equal(t, "hi", got[0].Feedback)
}

func TestExitSurveyNotMember(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	m.HandleModalSubmit(nil, surveySubmit("zed", sq.ID, "other", "x"))
	assert.Contains(t, editContent(api.lastEdit()), "not in this squad")
	got, _ := db.SquadExitSurveys(guild)
	assert.Empty(t, got)
}
