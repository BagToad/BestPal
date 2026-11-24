package squads

import (
	"errors"
	"fmt"
	"strings"
	"time"

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

	wantSurvey := reason != "" || feedback != ""

	lock := m.guildLock(i.GuildID)
	lock.Lock()
	var mates []string
	if wantSurvey {
		// Captured before leaving: the roster is gone once they're out.
		ids, err := m.db.SquadMemberIDs(id)
		if err != nil {
			m.config.Logger.Warnf("squads: exit survey roster: %v", err)
		}
		for _, mid := range ids {
			if mid != uid {
				mates = append(mates, mid)
			}
		}
	}
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
	if wantSurvey {
		m.recordExitSurvey(database.SquadExitSurvey{GuildID: i.GuildID, UserID: uid, SquadID: sq.ID, SquadName: sq.Name,
			Reason: reason, Feedback: feedback, CreatedAt: m.now()}, sq, mates)
	}
}

// recordExitSurvey saves answers and posts them, with enough context for
// mods to act on, to the feedback channel.
func (m *Module) recordExitSurvey(survey database.SquadExitSurvey, sq database.Squad, mates []string) {
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
	squad := fmt.Sprintf("%s · ID `%d` · formed <t:%d:R>", squadRef(sq), sq.ID, sq.CreatedAt.Unix())
	if sq.Members < 2 {
		// removeMember only starts the deletion; a failed one is retried.
		if _, exists, err := m.db.GetSquad(sq.ID); err == nil && !exists {
			squad += "\nDeleted: too few members left"
		} else {
			squad += "\nDeletion pending: too few members left"
		}
	}
	stay := "unknown"
	if joined, spoke, err := m.db.SquadLastMembership(sq.ID, survey.UserID); err == nil {
		spokeText := "👻 never spoke"
		if spoke {
			// Also set when chat history couldn't be checked, so leavers
			// aren't wrongly counted as ghosting.
			spokeText = "not counted as ghosting (spoke, or history couldn't be checked)"
		}
		stay = fmt.Sprintf("joined <t:%d:R> (%s) · %s", joined.Unix(), humanDuration(survey.CreatedAt.Sub(joined)), spokeText)
	}
	fields := []*discordgo.MessageEmbedField{
		logField("Member", idMention(survey.UserID), true),
		logField("Squad", squad, true),
		logField("Time in squad", stay, false),
		logField("Reason", reason, false),
		logField("Feedback", feedback, false),
		logField(fmt.Sprintf("Squadmates when they left (%d)", len(mates)), m.squadmateCompatibility(survey.GuildID, survey.UserID, mates), false),
	}
	for _, f := range fields {
		f.Value = truncate(f.Value, 1024)
	}
	if _, err := m.dc(nil).Send(ch, &discordgo.MessageSend{
		Embeds: []*discordgo.MessageEmbed{{
			Title:       "📝 Squad exit survey",
			Description: m.userSummary(survey.GuildID, survey.UserID, false),
			Color:       utils.Colors.Info(),
			Fields:      fields,
			Timestamp:   m.now().Format(time.RFC3339),
			Footer:      &discordgo.MessageEmbedFooter{Text: "Squads"},
		}},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("squads: post exit survey: %v", err)
	}
}

func idMention(uid string) string { return fmt.Sprintf("%s `%s`", mention(uid), uid) }

// squadmateCompatibility lists how someone lined up with each squadmate:
// shared games, region, age group and platforms.
func (m *Module) squadmateCompatibility(guildID, uid string, mates []string) string {
	if len(mates) == 0 {
		return "*None*"
	}
	rl := m.roleLookup(guildID)
	load := func(id string) (*candidate, []database.SquadPick, bool, error) {
		picks, err := m.db.SquadPicks(guildID, id)
		if err != nil {
			return nil, nil, false, err
		}
		c, gone, err := m.loadCandidate(guildID, id, picks, rl)
		return c, picks, gone, err
	}
	self, selfPicks, _, err := load(uid)
	if err != nil {
		lines := make([]string, len(mates))
		for n, id := range mates {
			lines[n] = "• " + idMention(id)
		}
		return strings.Join(lines, "\n") + "\n*Compatibility unavailable.*"
	}
	var lines []string
	for _, id := range mates {
		other, _, gone, err := load(id)
		switch {
		case gone:
			lines = append(lines, fmt.Sprintf("• %s — not in the server", idMention(id)))
			continue
		case err != nil:
			m.config.Logger.Warnf("squads: exit survey compatibility for %s: %v", id, err)
			lines = append(lines, fmt.Sprintf("• %s — compatibility unavailable", idMention(id)))
			continue
		}
		var shared []string
		for _, p := range selfPicks {
			if _, ok := other.Games[p.GameID]; ok {
				shared = append(shared, p.Name)
			}
		}
		games := "no shared games"
		if len(shared) > 0 {
			games = strings.Join(shared[:min(len(shared), 3)], ", ")
			if len(shared) > 3 {
				games += fmt.Sprintf(" +%d", len(shared)-3)
			}
		}
		region := "region unknown"
		switch {
		case self.Region != "" && self.Region == other.Region:
			region = "same region"
		case other.Region != "":
			region = regionName(other.Region)
		}
		age := "age unknown"
		switch {
		case self.Age > 0 && self.Age == other.Age:
			age = "same age group"
		case other.Age > 0:
			age = fmt.Sprintf("age group %d", other.Age)
		}
		var plats []string
		for _, p := range platforms {
			if self.Platforms[p.Code] && other.Platforms[p.Code] {
				plats = append(plats, p.Name)
			}
		}
		platText := "no shared platform"
		if len(plats) > 0 {
			platText = strings.Join(plats, ", ")
		}
		lines = append(lines, fmt.Sprintf("• %s — 🎮 %s · 🌍 %s · 🎂 %s · 🕹️ %s", idMention(id), games, region, age, platText))
	}
	return strings.Join(lines, "\n")
}
