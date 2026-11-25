package feud

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// The mod editor: /feud-admin edit browses questions, then a question's answer
// groups, with buttons to fix wording, adjust points, merge, hide and retire.
// Every action is checked for moderator access again, since the editor
// messages are ephemeral but their buttons are not otherwise guarded.

const (
	editPageSize = 20
	maxBonus     = 1000
	selectMax    = 25 // options in a Discord select menu
)

func editID(parts ...string) string { return prefix + "ed:" + strings.Join(parts, ":") }

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

func (m *Module) handleEditCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.ensureSeeded(i.GuildID)
	m.showEditList(s, i, 0)
}

// show replaces the editor message, or opens it if there isn't one yet.
func (m *Module) show(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	data.AllowedMentions = &discordgo.MessageAllowedMentions{}
	typ := discordgo.InteractionResponseUpdateMessage
	if i.Message == nil {
		typ = discordgo.InteractionResponseChannelMessageWithSource
		data.Flags = discordgo.MessageFlagsEphemeral
	}
	m.respond(s, i, typ, data)
}

func (m *Module) showEditList(s *discordgo.Session, i *discordgo.InteractionCreate, page int) {
	data, err := m.editListView(i.GuildID, page)
	if err != nil {
		m.config.Logger.Warnf("feud: edit list: %v", err)
		m.respondText(s, i, "❌ Couldn't load the questions.")
		return
	}
	m.show(s, i, data)
}

func statusEmoji(q database.FeudQuestion) string {
	switch q.Status {
	case database.FeudPending:
		return "🕓"
	case database.FeudRejected:
		return "🚫"
	}
	return "✅"
}

func (m *Module) editListView(guildID string, page int) (*discordgo.InteractionResponseData, error) {
	page = max(page, 0)
	qs, total, err := m.db.FeudQuestionsPage(guildID, page*editPageSize, editPageSize)
	if err != nil {
		return nil, err
	}
	if len(qs) == 0 && page > 0 {
		return m.editListView(guildID, 0)
	}
	pages := max((total+editPageSize-1)/editPageSize, 1)
	data := &discordgo.InteractionResponseData{
		Content: fmt.Sprintf("**GP Feud questions** (page %d of %d). ✅ active · 🕓 awaiting review · 🚫 retired", page+1, pages),
		Embeds:  []*discordgo.MessageEmbed{},
	}
	if len(qs) == 0 {
		data.Content = "No questions yet."
		data.Components = []discordgo.MessageComponent{}
		return data, nil
	}
	opts := make([]discordgo.SelectMenuOption, len(qs))
	for n, q := range qs {
		opts[n] = discordgo.SelectMenuOption{
			Label: truncate(q.Text, 100), Value: itoa(q.ID),
			Description: fmt.Sprintf("%d answers · #%d", q.Respondents, q.ID),
			Emoji:       &discordgo.ComponentEmoji{Name: statusEmoji(q)},
		}
	}
	data.Components = []discordgo.MessageComponent{
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: editID("pick"), Placeholder: "Pick a question to edit", Options: opts},
		}},
		discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.Button{Label: "Previous", Style: discordgo.SecondaryButton, CustomID: editID("list", strconv.Itoa(page-1)), Disabled: page == 0},
			discordgo.Button{Label: "Next", Style: discordgo.SecondaryButton, CustomID: editID("list", strconv.Itoa(page+1)), Disabled: page+1 >= pages},
		}},
	}
	return data, nil
}

func (m *Module) showQuestion(s *discordgo.Session, i *discordgo.InteractionCreate, qid int64, note string) {
	m.showQuestionPage(s, i, qid, 0, note)
}

func (m *Module) showQuestionPage(s *discordgo.Session, i *discordgo.InteractionCreate, qid int64, page int, note string) {
	data, err := m.questionView(i.GuildID, qid, page, note)
	if errors.Is(err, database.ErrFeudQuestionNotFound) {
		m.showEditList(s, i, 0)
		return
	}
	if err != nil {
		m.config.Logger.Warnf("feud: edit question: %v", err)
		m.respondText(s, i, "❌ Couldn't load that question.")
		return
	}
	m.show(s, i, data)
}

func (m *Module) questionView(guildID string, qid int64, page int, note string) (*discordgo.InteractionResponseData, error) {
	q, ok, err := m.db.FeudQuestion(guildID, qid)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, database.ErrFeudQuestionNotFound
	}
	answers, err := m.db.AllFeudAnswers(qid)
	if err != nil {
		return nil, err
	}
	var b strings.Builder
	lo, hi, pages, page := pageBounds(len(answers), page)
	shown := answers[lo:hi]
	for _, a := range shown {
		b.WriteString(answerLine(a) + "\n")
	}
	if b.Len() == 0 {
		b.WriteString("No answers yet.")
	}
	embed := &discordgo.MessageEmbed{
		Title: statusEmoji(q) + " " + q.Text, Color: feudColor,
		Description: truncate(b.String(), 4000),
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Question #%d · %d respondents · played %d times · answers page %d of %d",
			q.ID, q.Respondents, q.TimesPlayed, page+1, pages)},
	}
	data := &discordgo.InteractionResponseData{Content: note, Embeds: []*discordgo.MessageEmbed{embed}}
	var rows []discordgo.MessageComponent
	if len(shown) > 0 {
		opts := make([]discordgo.SelectMenuOption, len(shown))
		for n, a := range shown {
			opts[n] = discordgo.SelectMenuOption{Label: truncate(a.Label, 100), Value: itoa(a.ID),
				Description: fmt.Sprintf("%d points", a.Score())}
		}
		rows = append(rows, discordgo.ActionsRow{Components: []discordgo.MessageComponent{
			discordgo.SelectMenu{CustomID: editID("ans", itoa(qid)), Placeholder: "Pick an answer to edit", Options: opts},
		}})
	}
	qid64 := itoa(qid)
	buttons := []discordgo.MessageComponent{
		button("Edit question", "✏️", discordgo.PrimaryButton, editID("eq", qid64)),
		button("Add answer", "➕", discordgo.SecondaryButton, editID("aa", qid64)),
	}
	switch q.Status {
	case database.FeudActive:
		buttons = append(buttons,
			button("Tidy with agent", "🧹", discordgo.SecondaryButton, editID("dd", qid64)),
			button("Retire", "🚫", discordgo.DangerButton, editID("ret", qid64)))
	case database.FeudRejected:
		buttons = append(buttons, button("Restore", "♻️", discordgo.SuccessButton, editID("ret", qid64)))
	}
	nav := []discordgo.MessageComponent{button("Back to questions", "⬅️", discordgo.SecondaryButton, editID("list", "0"))}
	if pages > 1 {
		nav = append(nav,
			discordgo.Button{Label: "Earlier answers", Style: discordgo.SecondaryButton, CustomID: editID("q", qid64, strconv.Itoa(page-1)), Disabled: page == 0},
			discordgo.Button{Label: "More answers", Style: discordgo.SecondaryButton, CustomID: editID("q", qid64, strconv.Itoa(page+1)), Disabled: page+1 >= pages})
	}
	rows = append(rows, discordgo.ActionsRow{Components: buttons}, discordgo.ActionsRow{Components: nav})
	data.Components = rows
	return data, nil
}

// pageBounds clamps a page of select-menu sized answer lists.
func pageBounds(n, page int) (lo, hi, pages, clamped int) {
	pages = max((n+selectMax-1)/selectMax, 1)
	clamped = min(max(page, 0), pages-1)
	lo = clamped * selectMax
	return lo, min(lo+selectMax, n), pages, clamped
}

func answerLine(a database.FeudAnswer) string {
	line := fmt.Sprintf("**%s** · %d points (%d responses", a.Label, a.Score(), a.Count)
	if a.Extra != 0 {
		line += fmt.Sprintf(", %+d bonus", a.Extra)
	}
	line += ")"
	if a.Hidden {
		line += " 🙈 hidden"
	}
	return line
}

func (m *Module) showAnswer(s *discordgo.Session, i *discordgo.InteractionCreate, aid int64, note string) {
	a, err := m.db.FeudAnswerIn(i.GuildID, aid)
	if errors.Is(err, database.ErrFeudAnswerNotFound) {
		m.respondText(s, i, "That answer no longer exists.")
		return
	}
	if err != nil {
		m.config.Logger.Warnf("feud: edit answer: %v", err)
		m.respondText(s, i, "❌ Couldn't load that answer.")
		return
	}
	texts, _ := m.db.FeudResponseTexts(a.QuestionID)
	desc := answerLine(a)
	if ex := texts[a.ID]; len(ex) > 0 {
		if len(ex) > 15 {
			ex = ex[:15]
		}
		desc += "\nTyped as: " + truncate(strings.Join(ex, ", "), 500)
	}
	hideLabel, hideEmoji := "Hide from board", "🙈"
	if a.Hidden {
		hideLabel, hideEmoji = "Show on board", "👁️"
	}
	aid64 := itoa(a.ID)
	m.show(s, i, &discordgo.InteractionResponseData{
		Content: note,
		Embeds:  []*discordgo.MessageEmbed{{Title: "Answer", Description: desc, Color: feudColor}},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				button("Rename", "✏️", discordgo.PrimaryButton, editID("ra", aid64)),
				button("Set bonus points", "➕", discordgo.SecondaryButton, editID("bn", aid64)),
				button(hideLabel, hideEmoji, discordgo.SecondaryButton, editID("hd", aid64)),
				button("Merge into…", "🔀", discordgo.SecondaryButton, editID("mg", aid64)),
			}},
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				button("Back to question", "⬅️", discordgo.SecondaryButton, editID("q", itoa(a.QuestionID)))}},
		},
	})
}

func (m *Module) showMergeTargets(s *discordgo.Session, i *discordgo.InteractionCreate, aid int64, page int) {
	a, err := m.db.FeudAnswerIn(i.GuildID, aid)
	if err != nil {
		m.respondText(s, i, "That answer no longer exists.")
		return
	}
	all, err := m.db.AllFeudAnswers(a.QuestionID)
	if err != nil {
		m.respondText(s, i, "❌ Couldn't load the answers.")
		return
	}
	var others []database.FeudAnswer
	for _, o := range all {
		if o.ID != a.ID {
			others = append(others, o)
		}
	}
	aid64 := itoa(a.ID)
	nav := []discordgo.MessageComponent{button("Cancel", "⬅️", discordgo.SecondaryButton, editID("a", aid64))}
	if len(others) == 0 {
		m.show(s, i, &discordgo.InteractionResponseData{Content: "There's nothing to merge into.", Embeds: []*discordgo.MessageEmbed{},
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: nav}}})
		return
	}
	lo, hi, pages, page := pageBounds(len(others), page)
	opts := make([]discordgo.SelectMenuOption, 0, hi-lo)
	for _, o := range others[lo:hi] {
		opts = append(opts, discordgo.SelectMenuOption{Label: truncate(o.Label, 100), Value: itoa(o.ID), Description: fmt.Sprintf("%d points", o.Score())})
	}
	if pages > 1 {
		nav = append(nav,
			discordgo.Button{Label: "Earlier", Style: discordgo.SecondaryButton, CustomID: editID("mg", aid64, strconv.Itoa(page-1)), Disabled: page == 0},
			discordgo.Button{Label: "More", Style: discordgo.SecondaryButton, CustomID: editID("mg", aid64, strconv.Itoa(page+1)), Disabled: page+1 >= pages})
	}
	m.show(s, i, &discordgo.InteractionResponseData{
		Content: fmt.Sprintf("Merge **%s** into which answer? Its responses and bonus move over. (page %d of %d)", a.Label, page+1, pages),
		Embeds:  []*discordgo.MessageEmbed{},
		Components: []discordgo.MessageComponent{
			discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{CustomID: editID("mgto", aid64), Placeholder: "Merge into…", Options: opts}}},
			discordgo.ActionsRow{Components: nav},
		},
	})
}

func textModal(id, title, label, desc, value string, maxLen int) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{CustomID: id, Title: title, Components: []discordgo.MessageComponent{
		discordgo.Label{Label: label, Description: desc,
			Component: discordgo.TextInput{CustomID: "v", Style: discordgo.TextInputShort, Value: value,
				Required: new(true), MinLength: 1, MaxLength: maxLen}},
	}}
}

// handleEdit routes gf:ed: components.
func (m *Module) handleEdit(s *discordgo.Session, i *discordgo.InteractionCreate, parts []string) {
	if !m.canManage(i) {
		m.respondText(s, i, "❌ Only moderators can edit questions.")
		return
	}
	arg := func(n int) string {
		if n < len(parts) {
			return parts[n]
		}
		return ""
	}
	id, _ := strconv.ParseInt(arg(1), 10, 64)
	var picked int64
	if vals := i.MessageComponentData().Values; len(vals) > 0 {
		picked, _ = strconv.ParseInt(vals[0], 10, 64)
	}
	gid := i.GuildID
	switch arg(0) {
	case "list":
		m.showEditList(s, i, int(min(max(id, 0), 1<<20)))
	case "pick":
		m.showQuestion(s, i, picked, "")
	case "q":
		page, _ := strconv.Atoi(arg(2))
		m.showQuestionPage(s, i, id, page, "")
	case "ans":
		m.showAnswer(s, i, picked, "")
	case "a":
		m.showAnswer(s, i, id, "")
	case "eq":
		q, ok, err := m.db.FeudQuestion(gid, id)
		if err != nil || !ok {
			m.respondText(s, i, "That question no longer exists.")
			return
		}
		m.respond(s, i, discordgo.InteractionResponseModal,
			textModal(prefix+"edq:"+itoa(id), "Edit question", "Question", "", q.Text, maxQuestionLen))
	case "aa":
		d := textModal(prefix+"eda:"+itoa(id), "Add answer", "Answer", "", "", maxAnswerLen)
		d.Components = append(d.Components, discordgo.Label{Label: "Points", Description: "How many people this answer is worth.",
			Component: discordgo.TextInput{CustomID: "p", Style: discordgo.TextInputShort, Value: "1", Required: new(true), MinLength: 1, MaxLength: 4}})
		m.respond(s, i, discordgo.InteractionResponseModal, d)
	case "ra":
		a, err := m.db.FeudAnswerIn(gid, id)
		if err != nil {
			m.respondText(s, i, "That answer no longer exists.")
			return
		}
		m.respond(s, i, discordgo.InteractionResponseModal,
			textModal(prefix+"edr:"+itoa(id), "Rename answer", "Answer", "", a.Label, maxAnswerLen))
	case "bn":
		a, err := m.db.FeudAnswerIn(gid, id)
		if err != nil {
			m.respondText(s, i, "That answer no longer exists.")
			return
		}
		m.respond(s, i, discordgo.InteractionResponseModal, textModal(prefix+"edb:"+itoa(id), "Bonus points",
			"Bonus points", "Added to the response count. Use a negative number to subtract.", strconv.Itoa(a.Extra), 5))
	case "hd":
		a, err := m.db.FeudAnswerIn(gid, id)
		if err != nil {
			m.respondText(s, i, "That answer no longer exists.")
			return
		}
		if err := m.db.SetFeudAnswerHidden(a.QuestionID, a.ID, !a.Hidden); err != nil {
			m.respondText(s, i, "❌ Couldn't save that.")
			return
		}
		m.showAnswer(s, i, a.ID, "✅ Saved.")
	case "mg":
		page, _ := strconv.Atoi(arg(2))
		m.showMergeTargets(s, i, id, page)
	case "mgto":
		a, err := m.db.FeudAnswerIn(gid, id)
		if err != nil {
			m.respondText(s, i, "That answer no longer exists.")
			return
		}
		if err := m.db.MergeFeudAnswers(a.QuestionID, a.ID, picked); err != nil {
			m.respondText(s, i, "❌ Couldn't merge those.")
			return
		}
		m.showQuestion(s, i, a.QuestionID, fmt.Sprintf("✅ Merged **%s**.", a.Label))
	case "ret":
		q, ok, err := m.db.FeudQuestion(gid, id)
		if err != nil || !ok {
			m.respondText(s, i, "That question no longer exists.")
			return
		}
		retire := q.Status == database.FeudActive
		if err := m.db.SetFeudQuestionRetired(gid, id, retire); err != nil {
			m.respondText(s, i, "❌ That question can't change right now.")
			return
		}
		note := "✅ Question restored."
		if retire {
			note = "✅ Question retired."
		}
		m.showQuestion(s, i, id, note)
	case "dd":
		m.handleDedupeButton(s, i, id)
	}
}

// handleEditModal routes edq/eda/edr/edb modals.
func (m *Module) handleEditModal(s *discordgo.Session, i *discordgo.InteractionCreate, kind string, id int64) {
	if !m.canManage(i) {
		m.respondText(s, i, "❌ Only moderators can edit questions.")
		return
	}
	gid := i.GuildID
	text := cleanText(modalValue(i, "v"))
	switch kind {
	case "edq":
		text = truncate(text, maxQuestionLen)
		if len([]rune(text)) < minQuestionLen || !hasWord(text) {
			m.respondText(s, i, fmt.Sprintf("❌ Questions need at least %d characters.", minQuestionLen))
			return
		}
		err := m.db.UpdateFeudQuestionText(gid, id, text, normalize(text))
		switch {
		case errors.Is(err, database.ErrFeudDuplicateQuestion):
			m.respondText(s, i, "❌ Another question already says that.")
		case err != nil:
			m.config.Logger.Warnf("feud: edit question text: %v", err)
			m.respondText(s, i, "❌ Couldn't save that.")
		default:
			m.showQuestion(s, i, id, "✅ Question updated.")
		}
	case "eda":
		text = truncate(text, maxAnswerLen)
		pts, err := strconv.Atoi(strings.TrimSpace(modalValue(i, "p")))
		if !hasWord(text) || err != nil || pts < 1 || pts > maxBonus {
			m.respondText(s, i, fmt.Sprintf("❌ Give an answer and whole points from 1 to %d.", maxBonus))
			return
		}
		if _, ok, err := m.db.FeudQuestion(gid, id); err != nil || !ok {
			m.respondText(s, i, "That question no longer exists.")
			return
		}
		switch _, err := m.db.AddFeudAnswer(id, text, normalize(text), pts, m.now()); {
		case errors.Is(err, database.ErrFeudAnswerExists):
			m.respondText(s, i, "❌ That answer already exists.")
		case err != nil:
			m.config.Logger.Warnf("feud: add answer: %v", err)
			m.respondText(s, i, "❌ Couldn't save that.")
		default:
			m.showQuestion(s, i, id, "✅ Answer added.")
		}
	case "edr":
		a, err := m.db.FeudAnswerIn(gid, id)
		if err != nil {
			m.respondText(s, i, "That answer no longer exists.")
			return
		}
		text = truncate(text, maxAnswerLen)
		if !hasWord(text) {
			m.respondText(s, i, "❌ That answer needs some letters or numbers.")
			return
		}
		switch err := m.db.RenameFeudAnswer(a.QuestionID, a.ID, text, normalize(text)); {
		case errors.Is(err, database.ErrFeudAnswerExists):
			m.respondText(s, i, "❌ Another answer already uses that wording. Merge them instead.")
		case err != nil:
			m.config.Logger.Warnf("feud: rename answer: %v", err)
			m.respondText(s, i, "❌ Couldn't save that.")
		default:
			m.showAnswer(s, i, a.ID, "✅ Renamed.")
		}
	case "edb":
		a, err := m.db.FeudAnswerIn(gid, id)
		if err != nil {
			m.respondText(s, i, "That answer no longer exists.")
			return
		}
		n, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || n < -maxBonus || n > maxBonus {
			m.respondText(s, i, fmt.Sprintf("❌ Use a whole number from -%d to %d.", maxBonus, maxBonus))
			return
		}
		if err := m.db.SetFeudAnswerExtra(a.QuestionID, a.ID, n); err != nil {
			m.config.Logger.Warnf("feud: set bonus: %v", err)
			m.respondText(s, i, "❌ Couldn't save that.")
			return
		}
		m.showAnswer(s, i, a.ID, "✅ Bonus saved.")
	}
}

// startTidy stops two tidy-ups of the same question from running at once.
func (m *Module) startTidy(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.tidying == nil {
		m.tidying = map[string]bool{}
	}
	if m.tidying[key] {
		return false
	}
	m.tidying[key] = true
	return true
}

func (m *Module) endTidy(key string) {
	m.mu.Lock()
	delete(m.tidying, key)
	m.mu.Unlock()
}

func (m *Module) editResponse(i *discordgo.InteractionCreate, edit *discordgo.WebhookEdit) {
	if err := m.dc(nil).EditResponse(i.Interaction, edit); err != nil {
		m.config.Logger.Warnf("feud: edit response: %v", err)
	}
}

// handleDedupeButton has the agent tidy one question.
func (m *Module) handleDedupeButton(s *discordgo.Session, i *discordgo.InteractionCreate, qid int64) {
	if m.agent == nil {
		m.respondText(s, i, "The agent isn't running right now.")
		return
	}
	key := "q" + itoa(qid)
	if !m.startTidy(key) {
		m.respondText(s, i, "That question is already being tidied.")
		return
	}
	m.respond(s, i, discordgo.InteractionResponseDeferredMessageUpdate, nil)
	gid := i.GuildID
	m.spawn(func() {
		defer m.endTidy(key)
		actions, err := m.dedupeQuestion(context.Background(), gid, qid)
		note := "✅ Nothing needed tidying."
		switch {
		case err != nil:
			m.config.Logger.Warnf("feud: tidy question %d: %v", qid, err)
			note = "❌ The agent couldn't finish."
			if len(actions) > 0 {
				note += " Changes made so far:\n• " + truncate(strings.Join(actions, "\n• "), 1200)
			}
		case len(actions) > 0:
			note = "✅ Tidied:\n• " + truncate(strings.Join(actions, "\n• "), 1500)
		}
		data, verr := m.questionView(gid, qid, 0, note)
		if verr != nil {
			m.editResponse(i, &discordgo.WebhookEdit{Content: &note})
			return
		}
		m.editResponse(i, &discordgo.WebhookEdit{Content: &data.Content, Embeds: &data.Embeds, Components: &data.Components})
	})
}

// handleDedupe has the agent tidy every playable question.
func (m *Module) handleDedupe(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.agent == nil {
		m.respondText(s, i, "The agent isn't running right now.")
		return
	}
	key := "guild:" + i.GuildID
	if !m.startTidy(key) {
		m.respondText(s, i, "A tidy-up is already running.")
		return
	}
	m.respond(s, i, discordgo.InteractionResponseDeferredChannelMessageWithSource,
		&discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral})
	gid := i.GuildID
	m.spawn(func() {
		defer m.endTidy(key)
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
		defer cancel()
		out, err := m.dedupeGuild(ctx, gid)
		if err != nil {
			m.config.Logger.Warnf("feud: tidy guild: %v", err)
			out = "❌ Couldn't tidy the questions."
		}
		m.editResponse(i, &discordgo.WebhookEdit{Content: &out})
	})
}
