package feud

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

// boardEmbed draws the game board. A nil game shows the idle board.
func boardEmbed(g *Game, _ database.FeudBoard) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{Title: "GP Feud", Color: feudColor,
		Footer: &discordgo.MessageEmbedFooter{Text: "Every answer on the board came from server members."}}
	if g == nil || g.Phase == phaseEnded {
		e.Description = "No game right now. Answer survey questions so the next one is ready!"
		if g != nil {
			e.Title = "GP Feud · Game over"
			e.Description = fmt.Sprintf("## %s\n🔴 Red **%d** · 🔵 Blue **%d**\n\n%s",
				g.Result, g.Score[red], g.Score[blue], e.Description)
		}
		return e
	}
	var d strings.Builder
	switch {
	case g.Phase == phaseLobby:
		e.Title = "GP Feud · Starting soon"
		d.WriteString("Press **Join game** to get put on a team.\n")
		if g.Deadline.IsZero() {
			d.WriteString("The first round starts once both teams have a player.")
		} else {
			fmt.Fprintf(&d, "First round <t:%d:R>.", g.Deadline.Unix())
		}
	default:
		e.Title = fmt.Sprintf("GP Feud · Round %d of %d", g.Round, g.Rounds)
		fmt.Fprintf(&d, "## %s\n", g.Question)
		if g.Last != "" {
			d.WriteString(g.Last + "\n")
		}
		d.WriteString(statusLine(g))
		e.Fields = slotFields(g.Slots)
	}
	e.Description = d.String()
	for _, team := range []int{red, blue} {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s %s · %d", teamEmoji(team), teamNames[team], g.Score[team]),
			Value:  strikes(g.Strikes[team], g.Round > 0) + roster(g.members(team)),
			Inline: true,
		})
	}
	return e
}

func statusLine(g *Game) string {
	t := fmt.Sprintf("%s %s", teamEmoji(g.Turn), teamNames[g.Turn])
	switch g.Phase {
	case phaseTurn:
		return fmt.Sprintf("▶ **%s's turn**, answering in their thread. Time's up <t:%d:R>.", t, g.Deadline.Unix())
	case phaseJudging:
		return fmt.Sprintf("🔒 %s locked in **%s**…", t, g.Locked)
	case phaseBreak:
		return fmt.Sprintf("Round over! Next round <t:%d:R>.", g.Deadline.Unix())
	case phasePaused:
		return fmt.Sprintf("⏸ Paused: a team has no players. Join to keep it going! The game ends <t:%d:R>.", g.Deadline.Unix())
	}
	return ""
}

// slotFields lays the answers out as two columns, 1–5 and 6–10.
func slotFields(slots []Slot) []*discordgo.MessageEmbedField {
	if len(slots) == 0 {
		return nil
	}
	col := func(from, to int) string {
		var b strings.Builder
		for n := from; n < to && n < len(slots); n++ {
			s := slots[n]
			switch {
			case !s.Shown:
				fmt.Fprintf(&b, "`%2d` ▬▬▬▬▬▬\n", n+1)
			case s.By < 0:
				fmt.Fprintf(&b, "`%2d` *%s · %d*\n", n+1, s.Label, s.Points)
			default:
				fmt.Fprintf(&b, "`%2d` %s **%s · %d**\n", n+1, teamEmoji(s.By), s.Label, s.Points)
			}
		}
		return b.String()
	}
	half := min(5, len(slots))
	fields := []*discordgo.MessageEmbedField{{Name: "\u200b", Value: col(0, half), Inline: true}}
	if len(slots) > half {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "\u200b", Value: col(half, len(slots)), Inline: true})
	}
	// Ends the row so the team fields sit together below the answers.
	return append(fields, &discordgo.MessageEmbedField{Name: "\u200b", Value: "\u200b", Inline: false})
}

func strikes(n int, show bool) string {
	if !show {
		return ""
	}
	return strings.Repeat("✖", n) + strings.Repeat("▫", maxStrikes-n) + "\n"
}

func roster(ids []string) string {
	if len(ids) == 0 {
		return "*Nobody yet*"
	}
	const maxShown = 15
	var b strings.Builder
	for n, id := range ids {
		if n == maxShown {
			fmt.Fprintf(&b, "+%d more", len(ids)-maxShown)
			break
		}
		b.WriteString("<@" + id + ">\n")
	}
	return b.String()
}

func boardComponents() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		button("Join game", "🎮", discordgo.SuccessButton, prefix+"join"),
		button("Leave", "🚪", discordgo.SecondaryButton, prefix+"leave"),
		answerButton("Answer questions"),
		submitButton(),
	}}}
}

// panelContent is the turn panel's text in the team thread.
func panelContent(g *Game) string {
	return fmt.Sprintf("%s **%s, your turn!** Time's up <t:%d:R>.\n## %s\nSuggest an answer or vote for one. More than half the team locks it in; otherwise the top pick goes in when time's up.",
		teamEmoji(g.Turn), teamNames[g.Turn], g.Deadline.Unix(), g.Question)
}

// panelComponents are the Suggest button and one vote button per
// suggestion, most votes first.
func panelComponents(g *Game) []discordgo.MessageComponent {
	seq := strconv.Itoa(g.TurnSeq)
	rows := []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		button("Suggest an answer", "💡", discordgo.PrimaryButton, prefix+"suggest:"+seq),
	}}}
	votes := g.tally()
	sugs := append([]Suggestion(nil), g.Suggestions...)
	sort.SliceStable(sugs, func(i, j int) bool { return votes[sugs[i].ID] > votes[sugs[j].ID] })
	var row []discordgo.MessageComponent
	for n, s := range sugs {
		style := discordgo.SecondaryButton
		if n == 0 {
			style = discordgo.SuccessButton
		}
		label := truncate(s.Text, 70) + " · " + strconv.Itoa(votes[s.ID])
		row = append(row, discordgo.Button{Label: label, Style: style, CustomID: prefix + "vote:" + seq + ":" + strconv.Itoa(s.ID)})
		if len(row) == 5 {
			rows = append(rows, discordgo.ActionsRow{Components: row})
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, discordgo.ActionsRow{Components: row})
	}
	return rows
}
