package squads

import (
	"encoding/json"
	"fmt"
	"testing"

	"gamerpal/internal/config"
	"gamerpal/internal/discordcompat"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func surveySubmit(uid string, squadID int64, reason, feedback string) *discordgo.InteractionCreate {
	return &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
		Type: discordgo.InteractionModalSubmit, GuildID: guild,
		Member: &discordgo.Member{User: &discordgo.User{ID: uid}},
		Data: discordgo.ModalSubmitInteractionData{CustomID: fmt.Sprintf("%ssurvey:%d:Squad Axolotl", prefix, squadID), Components: []discordgo.MessageComponent{
			&discordgo.TextDisplay{},
			&discordgo.Label{Component: &discordgo.TextInput{CustomID: surveyReasonID, Value: reason}},
			&discordgo.Label{Component: &discordgo.TextInput{CustomID: surveyFeedbackID, Value: feedback}},
			&discordgo.TextDisplay{},
		}},
	}}
}

func TestLeaveConfirmOpensSurveyAndLeaves(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleave:%d", prefix, sq.ID)))
	assert.Contains(t, api.lastResponse().Data.Content, "Leave **Squad Axolotl**?")
	_, inSquad, _ := db.SquadForUser(guild, "a")
	assert.True(t, inSquad, "the Leave button only asks")

	m.HandleComponent(nil, component("a", fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)))
	r := api.lastResponse()
	require.Equal(t, discordgo.InteractionResponseModal, r.Type)
	assert.Equal(t, fmt.Sprintf("%ssurvey:%d:Squad Axolotl", prefix, sq.ID), r.Data.CustomID)
	assert.LessOrEqual(t, len(r.Data.Title), 45)
	require.Len(t, r.Data.Components, 4)
	assert.Contains(t, r.Data.Components[0].(discordgo.TextDisplay).Content, "Cancel skips feedback submission and leaves the squad.")

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

	_, inSquad, _ = db.SquadForUser(guild, "a")
	assert.False(t, inSquad, "left without waiting for the survey, so Cancel still leaves")
	got, _ := db.SquadExitSurveys(guild)
	assert.Empty(t, got)
}

func TestSquadLeaveCommandAsksToConfirm(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	makeSquad(t, m, api, db, "a", "b", "c")
	m.handleSquad(nil, command("a", 0, "squad", "leave"))
	assert.Contains(t, editContent(api.lastEdit()), "Leave **Squad Axolotl**?")
}

func TestLeaveConfirmNotMember(t *testing.T) {
	m, api, db, _ := newTestModule(t, nil)
	sq := makeSquad(t, m, api, db, "a", "b", "c")
	m.HandleComponent(nil, component("zed", fmt.Sprintf("%sleaveyes:%d", prefix, sq.ID)))
	r := api.lastResponse()
	assert.Equal(t, discordgo.InteractionResponseUpdateMessage, r.Type)
	assert.Contains(t, r.Data.Content, "not in this squad")
}

func TestExitSurveySubmitRecords(t *testing.T) {
	m, api, db, _ := newTestModule(t, map[string]any{config.KeySquadsFeedbackChannelID: "feedback"})
	m.HandleModalSubmit(nil, surveySubmit("a", 7, "no_response", "  nobody ever talked  "))
	assert.Contains(t, api.lastResponse().Data.Content, "Thanks for the feedback")

	got, err := db.SquadExitSurveys(guild)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "a", got[0].UserID)
	assert.Equal(t, int64(7), got[0].SquadID)
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
	var fields []string
	for _, f := range post.Embeds[0].Fields {
		fields = append(fields, f.Name+": "+f.Value)
	}
	assert.Equal(t, []string{"Member: <@a>", "Squad: Squad Axolotl", "Reason: Nobody is responding", "Feedback: nobody ever talked"}, fields)
	assert.NotNil(t, post.AllowedMentions, "doesn't ping")
}

func TestExitSurveyBlankRecordsNothing(t *testing.T) {
	m, api, db, _ := newTestModule(t, map[string]any{config.KeySquadsFeedbackChannelID: "feedback"})
	m.HandleModalSubmit(nil, surveySubmit("a", 7, "", " "))
	assert.Contains(t, api.lastResponse().Data.Content, "You left **Squad Axolotl**")
	got, err := db.SquadExitSurveys(guild)
	require.NoError(t, err)
	assert.Empty(t, got)
	for _, s := range api.sent {
		assert.NotEqual(t, "feedback", s.ChannelID)
	}
}

func TestExitSurveyWithoutChannelStillSaves(t *testing.T) {
	m, _, db, _ := newTestModule(t, nil)
	m.HandleModalSubmit(nil, surveySubmit("a", 7, "bogus", "hi"))
	got, err := db.SquadExitSurveys(guild)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Empty(t, got[0].Reason, "unknown reasons are dropped")
	assert.Equal(t, "hi", got[0].Feedback)
}
