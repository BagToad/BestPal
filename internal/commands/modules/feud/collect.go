package feud

import (
	"errors"
	"fmt"
	"strconv"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

const (
	maxAnswerLen     = 60
	minQuestionLen   = 10
	maxQuestionLen   = 150
	maxPendingByUser = 3

	answerInputID   = "answer"
	questionInputID = "question"
)

// ensureSeeded adds the starter questions once per guild.
func (m *Module) ensureSeeded(guildID string) {
	if _, ok := m.seeded.Load(guildID); ok {
		return
	}
	if _, err := m.db.SeedFeudQuestions(guildID, seedQuestions(), m.now()); err != nil {
		m.config.Logger.Warnf("feud: seed questions: %v", err)
		return
	}
	m.seeded.Store(guildID, struct{}{})
}

func seedQuestions() []database.FeudSeed {
	out := make([]database.FeudSeed, len(seeds))
	for n, q := range seeds {
		out[n] = database.FeudSeed{Text: q, Norm: normalize(q)}
	}
	return out
}

// nextQuestion picks what to ask a member next: the unanswered question
// closest to being playable, so questions fill up one at a time, then any
// playable one (more answers make a better board).
func (m *Module) nextQuestion(guildID, uid string) (database.FeudQuestion, bool, error) {
	qs, err := m.db.FeudQuestionsToAnswer(guildID, uid)
	if err != nil || len(qs) == 0 {
		return database.FeudQuestion{}, false, err
	}
	need := m.config.ForGuild(guildID).GetFeudMinRespondents()
	var best []database.FeudQuestion
	for _, q := range qs {
		if q.Respondents >= need {
			continue
		}
		switch {
		case len(best) == 0 || q.Respondents > best[0].Respondents:
			best = []database.FeudQuestion{q}
		case q.Respondents == best[0].Respondents:
			best = append(best, q)
		}
	}
	if len(best) == 0 {
		best = qs
	}
	return best[m.randN(len(best))], true, nil
}

// handleAnswerButton opens the answer form for the next question.
func (m *Module) handleAnswerButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.ensureSeeded(i.GuildID)
	q, ok, err := m.nextQuestion(i.GuildID, userID(i))
	if err != nil {
		m.config.Logger.Warnf("feud: next question: %v", err)
		m.respondText(s, i, "❌ Couldn't load a question. Try again in a moment.")
		return
	}
	if !ok {
		m.respondText(s, i, "🎉 You've answered every question for now. Got a good one of your own?",
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{submitButton()}})
		return
	}
	m.respond(s, i, discordgo.InteractionResponseModal, answerModal(q))
}

func answerModal(q database.FeudQuestion) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		CustomID: prefix + "ans:" + strconv.FormatInt(q.ID, 10),
		Title:    "GP Feud survey",
		Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: "### " + q.Text},
			discordgo.Label{Label: "Your answer", Description: "Go with the first thing that comes to mind. Answers are anonymous.",
				Component: discordgo.TextInput{CustomID: answerInputID, Style: discordgo.TextInputShort,
					Required: new(true), MinLength: 1, MaxLength: maxAnswerLen}},
		},
	}
}

func answerButton(label string) discordgo.Button {
	return button(label, "📝", discordgo.PrimaryButton, prefix+"answer")
}

func submitButton() discordgo.Button {
	return button("Submit a question", "💡", discordgo.SecondaryButton, prefix+"submit")
}

// handleAnswerSubmit saves an answer and offers the next question.
func (m *Module) handleAnswerSubmit(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string) {
	qid, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return
	}
	text := truncate(cleanText(modalValue(i, answerInputID)), maxAnswerLen)
	norm := normalize(text)
	if !hasWord(text) || norm == "" {
		m.respondText(s, i, "❌ That answer needs some letters or numbers.")
		return
	}
	_, err = m.db.AddFeudResponse(i.GuildID, qid, userID(i), text, norm, m.now())
	next := discordgo.ActionsRow{Components: []discordgo.MessageComponent{answerButton("Answer another")}}
	switch {
	case errors.Is(err, database.ErrFeudAlreadyAnswered):
		m.respondText(s, i, "You already answered that one.", next)
	case errors.Is(err, database.ErrFeudNotActive):
		m.respondText(s, i, "That question was retired. Try another.", next)
	case err != nil:
		m.config.Logger.Warnf("feud: save answer: %v", err)
		m.respondText(s, i, "❌ Couldn't save your answer. Try again in a moment.")
	default:
		m.respondText(s, i, "✅ Answer saved. Thanks!", next)
	}
}

func (m *Module) openSubmitModal(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.respond(s, i, discordgo.InteractionResponseModal, &discordgo.InteractionResponseData{
		CustomID: prefix + "sub",
		Title:    "Submit a GP Feud question",
		Components: []discordgo.MessageComponent{
			discordgo.TextDisplay{Content: "Ask something lots of people can answer in a word or two, like *Name a game everyone has played.* Mods review every question."},
			discordgo.Label{Label: "Your question",
				Component: discordgo.TextInput{CustomID: questionInputID, Style: discordgo.TextInputShort,
					Required: new(true), MinLength: minQuestionLen, MaxLength: maxQuestionLen,
					Placeholder: "Name something…"}},
		},
	})
}

// handleSubmitQuestion stores a member's question and sends it for review.
func (m *Module) handleSubmitQuestion(s *discordgo.Session, i *discordgo.InteractionCreate) {
	text := truncate(cleanText(modalValue(i, questionInputID)), maxQuestionLen)
	if len([]rune(text)) < minQuestionLen || !hasWord(text) {
		m.respondText(s, i, fmt.Sprintf("❌ Questions need at least %d characters.", minQuestionLen))
		return
	}
	uid := userID(i)
	m.ensureSeeded(i.GuildID)
	pending, err := m.db.CountFeudPendingBy(i.GuildID, uid)
	if err != nil {
		m.config.Logger.Warnf("feud: count pending: %v", err)
		m.respondText(s, i, "❌ Couldn't save your question. Try again in a moment.")
		return
	}
	if pending >= maxPendingByUser {
		m.respondText(s, i, fmt.Sprintf("You already have %d questions waiting for review. Try again once mods get to them.", pending))
		return
	}
	id, err := m.db.SubmitFeudQuestion(i.GuildID, uid, text, normalize(text), m.now())
	if errors.Is(err, database.ErrFeudDuplicateQuestion) {
		m.respondText(s, i, "Someone already asked that one!")
		return
	}
	if err != nil {
		m.config.Logger.Warnf("feud: submit question: %v", err)
		m.respondText(s, i, "❌ Couldn't save your question. Try again in a moment.")
		return
	}
	m.respondText(s, i, "✅ Thanks! Mods will review your question.")
	m.postForReview(s, i.GuildID, id)
}

// postForReview posts a new question to the review channel, if one is set.
func (m *Module) postForReview(s *discordgo.Session, guildID string, id int64) {
	ch := m.config.ForGuild(guildID).GetFeudReviewChannelID()
	if ch == "" {
		return
	}
	q, ok, err := m.db.FeudQuestion(guildID, id)
	if err != nil || !ok {
		return
	}
	msg, err := m.dc(s).Send(ch, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{reviewEmbed(q)},
		Components:      reviewButtons(q.ID, false),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		m.config.Logger.Warnf("feud: post question for review: %v", err)
		return
	}
	if err := m.db.SetFeudQuestionReviewMessage(q.ID, ch, msg.ID); err != nil {
		m.config.Logger.Warnf("feud: save review message: %v", err)
	}
}

func reviewEmbed(q database.FeudQuestion) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title:       "GP Feud question",
		Description: q.Text,
		Color:       feudColor,
		Fields:      []*discordgo.MessageEmbedField{{Name: "Submitted by", Value: "<@" + q.SubmittedBy + ">", Inline: true}},
	}
	switch q.Status {
	case database.FeudActive:
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Status", Value: "✅ Approved by <@" + q.ReviewedBy + ">", Inline: true})
		e.Color = 0x57F287
	case database.FeudRejected:
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Status", Value: "❌ Rejected by <@" + q.ReviewedBy + ">", Inline: true})
		e.Color = 0xED4245
	}
	return e
}

// reviewButtons are the Approve/Reject buttons. In the /feud-admin review
// flow (next=true) a decision moves on to the next pending question.
func reviewButtons(id int64, next bool) []discordgo.MessageComponent {
	suffix := ""
	if next {
		suffix = ":next"
	}
	ids := strconv.FormatInt(id, 10) + suffix
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		button("Approve", "✅", discordgo.SuccessButton, prefix+"approve:"+ids),
		button("Reject", "✖️", discordgo.DangerButton, prefix+"reject:"+ids),
	}}}
}

// handleReview approves or rejects a question from a review post or the
// /feud-admin review flow.
func (m *Module) handleReview(s *discordgo.Session, i *discordgo.InteractionCreate, idStr string, approve, next bool) {
	if !m.canManage(i) {
		m.respondText(s, i, "❌ Only moderators can review questions.")
		return
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		return
	}
	q, err := m.db.ReviewFeudQuestion(i.GuildID, id, approve, userID(i), m.now())
	already := errors.Is(err, database.ErrFeudNotPending)
	if err != nil && !already {
		m.config.Logger.Warnf("feud: review question: %v", err)
		m.respondText(s, i, "❌ Couldn't save that. Try again in a moment.")
		return
	}
	note := ""
	if already {
		note = "Someone already reviewed that one."
	}
	if next {
		m.syncReviewPost(s, q)
		m.handleReviewNext(s, i, note, true)
		return
	}
	m.respond(s, i, discordgo.InteractionResponseUpdateMessage, &discordgo.InteractionResponseData{
		Embeds:     []*discordgo.MessageEmbed{reviewEmbed(q)},
		Components: []discordgo.MessageComponent{},
	})
}

// syncReviewPost updates the review channel post after a decision made
// elsewhere.
func (m *Module) syncReviewPost(s *discordgo.Session, q database.FeudQuestion) {
	if q.ReviewMessageID == "" || q.Status == database.FeudPending {
		return
	}
	comps := []discordgo.MessageComponent{}
	if _, err := m.dc(s).Edit(&discordgo.MessageEdit{
		Channel: q.ReviewChannelID, ID: q.ReviewMessageID,
		Embeds: &[]*discordgo.MessageEmbed{reviewEmbed(q)}, Components: &comps,
	}); err != nil {
		m.config.Logger.Warnf("feud: update review post: %v", err)
	}
}

// handleReviewNext shows the oldest pending question with review buttons.
func (m *Module) handleReviewNext(s *discordgo.Session, i *discordgo.InteractionCreate, note string, update bool) {
	q, ok, err := m.db.OldestPendingFeudQuestion(i.GuildID)
	data := &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral, AllowedMentions: &discordgo.MessageAllowedMentions{}}
	switch {
	case err != nil:
		m.config.Logger.Warnf("feud: next pending question: %v", err)
		data.Content = "❌ Couldn't load the review queue."
	case !ok:
		data.Content = "✅ No questions waiting for review."
		data.Embeds = []*discordgo.MessageEmbed{}
		data.Components = []discordgo.MessageComponent{}
	default:
		data.Embeds = []*discordgo.MessageEmbed{reviewEmbed(q)}
		data.Components = reviewButtons(q.ID, true)
	}
	if note != "" {
		data.Content = note + "\n" + data.Content
	}
	typ := discordgo.InteractionResponseChannelMessageWithSource
	if update {
		typ = discordgo.InteractionResponseUpdateMessage
	}
	m.respond(s, i, typ, data)
}

// handlePool shows the size of the question pool.
func (m *Module) handlePool(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.ensureSeeded(i.GuildID)
	need := m.config.ForGuild(i.GuildID).GetFeudMinRespondents()
	st, err := m.db.FeudPoolStats(i.GuildID, need)
	if err != nil {
		m.config.Logger.Warnf("feud: pool stats: %v", err)
		m.respondText(s, i, "❌ Couldn't load the question pool.")
		return
	}
	m.respondText(s, i, fmt.Sprintf("**GP Feud questions**\n- Ready to play: **%d**\n- Collecting answers: **%d** (need %d each)\n- Awaiting review: **%d**",
		st.Ready, st.Collecting, need, st.Pending))
}
