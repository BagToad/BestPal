package squads

import (
	"fmt"
	"strings"

	"gamerpal/internal/database"
	"gamerpal/internal/discordcompat"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// Confirming a leave removes you from the squad and opens an optional exit
// survey. Submitting sends feedback; Cancel just skips it.

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
			// The squad may be deleted by the time this is submitted, so the
			// name rides along in the custom ID (max 100 characters).
			CustomID: fmt.Sprintf("%ssurvey:%d:%s", prefix, sq.ID, truncate(sq.Name, 60)),
			Title:    truncate("Leave "+sq.Name, 45),
			Components: []discordgo.MessageComponent{
				discordgo.TextDisplay{Content: fmt.Sprintf("Leaving **%s**. Mind telling us why? Everything here is optional.\n"+
					"**Cancel skips feedback submission and leaves the squad.**", sq.Name)},
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

// handleExitSurveySubmit records answers from someone who already left.
func (m *Module) handleExitSurveySubmit(s *discordgo.Session, i *discordgo.InteractionCreate, idStr, squadName string) {
	id, _ := parseID(idStr)
	reason := modalValue(i, surveyReasonID)
	if surveyReasonLabel(reason) == "" {
		reason = ""
	}
	feedback := truncate(strings.TrimSpace(modalValue(i, surveyFeedbackID)), maxSurveyText)
	if reason == "" && feedback == "" {
		m.respondText(s, i, fmt.Sprintf("👋 You left **%s**. Want a new squad? Run `/squad join`.", squadName))
		return
	}
	m.respondText(s, i, fmt.Sprintf("👋 You left **%s**. Thanks for the feedback! Want a new squad? Run `/squad join`.", squadName))
	m.recordExitSurvey(database.SquadExitSurvey{GuildID: i.GuildID, UserID: userID(i), SquadID: id, SquadName: squadName,
		Reason: reason, Feedback: feedback, CreatedAt: m.now()})
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
