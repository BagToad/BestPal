package squads

import (
	"errors"
	"fmt"
	"strings"

	"gamerpal/internal/database"
	"gamerpal/internal/discordcompat"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// Leaving a squad goes through an optional exit survey. Everything in it is
// optional, so submitting it blank just leaves. Discord's Cancel keeps you in
// the squad.

const (
	surveyReasonID   = "reason"
	surveyFeedbackID = "feedback"
	maxSurveyText    = 1000
)

var surveyReasons = []discordcompat.RadioOption{
	{Value: "poor_match", Label: "This isn't a good match"},
	{Value: "no_response", Label: "Nobody is responding"},
	{Value: "other", Label: "Other"},
}

func surveyReasonLabel(value string) string {
	for _, r := range surveyReasons {
		if r.Value == value {
			return r.Label
		}
	}
	return ""
}

// openExitSurvey shows the leave form for a squad.
func (m *Module) openExitSurvey(s *discordgo.Session, i *discordgo.InteractionCreate, sq database.Squad) {
	if err := m.dc(s).Respond(i.Interaction, exitSurvey(sq)); err != nil {
		m.config.Logger.Warnf("squads: open exit survey: %v", err)
	}
}

func exitSurvey(sq database.Squad) *discordgo.InteractionResponse {
	return &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: fmt.Sprintf("%ssurvey:%d", prefix, sq.ID),
			Title:    truncate("Leave "+sq.Name, 45),
			Components: []discordgo.MessageComponent{
				discordgo.TextDisplay{Content: fmt.Sprintf("You'll lose access to **%s**'s channel. Mind telling us why? "+
					"It's optional: leave it blank and hit Submit to just leave.", sq.Name)},
				discordgo.Label{Label: "Why are you leaving?", Component: discordcompat.RadioGroup{
					CustomID: surveyReasonID, Options: surveyReasons, Required: new(false),
				}},
				discordgo.Label{Label: "Anything else you'd like to tell us?", Component: discordgo.TextInput{
					CustomID: surveyFeedbackID, Style: discordgo.TextInputParagraph, Required: new(false), MaxLength: maxSurveyText,
					Placeholder: "Reasons for leaving, feedback, ideas…",
				}},
				discordgo.TextDisplay{Content: "-# This information is shared privately with app developers."},
			},
		},
	}
}

// handleExitSurveySubmit leaves the squad and records any answers.
func (m *Module) handleExitSurveySubmit(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string) {
	id, _ := parseID(idStr)
	if !m.deferReply(s, i) {
		return
	}
	uid := userID(i)
	reason := modalValue(i, surveyReasonID)
	if surveyReasonLabel(reason) == "" {
		reason = ""
	}
	feedback := truncate(strings.TrimSpace(modalValue(i, surveyFeedbackID)), maxSurveyText)

	lock := m.guildLock(i.GuildID)
	lock.Lock()
	sq, err := m.removeMember(i.GuildID, uid, id, database.SquadLeaveVoluntary, "left")
	lock.Unlock()
	switch {
	case errors.Is(err, errNotMember):
		m.editText(s, i, "You're not in this squad anymore.")
		return
	case err != nil:
		m.config.Logger.Errorf("squads: leave: %v", err)
		m.editText(s, i, "❌ Could not leave the squad. Try again in a moment.")
		return
	}
	m.editText(s, i, fmt.Sprintf("👋 You left **%s**. Want a new squad? Run `/squad join`.", sq.Name))
	if reason != "" || feedback != "" {
		m.recordExitSurvey(database.SquadExitSurvey{GuildID: i.GuildID, UserID: uid, SquadID: sq.ID, SquadName: sq.Name,
			Reason: reason, Feedback: feedback, CreatedAt: m.now()})
	}
}

func (m *Module) recordExitSurvey(survey database.SquadExitSurvey) {
	if err := m.db.AddSquadExitSurvey(survey); err != nil {
		m.config.Logger.Errorf("squads: save exit survey: %v", err)
	}
	ch := m.config.ForGuild(survey.GuildID).GetSquadsFeedbackChannelID()
	if ch == "" {
		return
	}
	reason := surveyReasonLabel(survey.Reason)
	if reason == "" {
		reason = "*Not picked*"
	}
	feedback := survey.Feedback
	if feedback == "" {
		feedback = "*None*"
	}
	if err := m.modLogFilesTo(ch, "📝 Squad exit survey", utils.Colors.Info(), nil,
		logField("Member", mention(survey.UserID), true),
		logField("Squad", survey.SquadName, true),
		logField("Reason", reason, false),
		logField("Feedback", feedback, false),
	); err != nil {
		m.config.Logger.Warnf("squads: post exit survey: %v", err)
	}
}
