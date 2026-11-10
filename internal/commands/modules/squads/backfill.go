package squads

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"gamerpal/internal/forumcache"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

var (
	minBackfillDays float64 = 1
	maxBackfillDays float64 = 365
	// backfillPause spaces out posts so a big backfill doesn't flood the forum.
	backfillPause = 500 * time.Millisecond
)

const backfillProgressEvery = 25

type backfillResult struct {
	candidates, posted, hasButton, inSquads, left, failed int
}

// adminBackfill checks a backfill can run, then runs it in the background
// because a large forum can take longer than an interaction usually waits.
func (m *Module) adminBackfill(s *discordgo.Session, i *discordgo.InteractionCreate, days int, dry bool) {
	gc := m.config.ForGuild(i.GuildID)
	switch {
	case days < int(minBackfillDays) || days > int(maxBackfillDays):
		m.respondText(s, i, fmt.Sprintf("❌ Pick between %d and %d days.", int(minBackfillDays), int(maxBackfillDays)))
		return
	case !gc.GetSquadsEnabled():
		m.respondText(s, i, "❌ Squads are off, so the Join a squad button wouldn't work.")
		return
	case gc.GetGamerPalsIntroductionsForumChannelID() == "" || m.introThreads == nil:
		m.respondText(s, i, "❌ The introductions forum isn't set up or cached yet.")
		return
	}
	if _, running := m.backfills.LoadOrStore(i.GuildID, struct{}{}); running {
		m.respondText(s, i, "⏳ An intro backfill is already running. Wait for it to finish.")
		return
	}
	if !m.deferReply(s, i) {
		m.backfills.Delete(i.GuildID)
		return
	}
	go m.backfillIntros(s, i, days, dry)
}

// backfillIntros posts the Join a squad button on each member's latest intro
// from the last days days, skipping people who already have one, are queued
// or in a squad, or have left.
func (m *Module) backfillIntros(s *discordgo.Session, i *discordgo.InteractionCreate, days int, dry bool) {
	defer m.backfills.Delete(i.GuildID)
	guildID := i.GuildID
	api := m.dc(s)
	threads := latestIntros(m.introThreads(guildID), m.now().AddDate(0, 0, -days), api.BotID())

	var r backfillResult
	r.candidates = len(threads)
	for n, th := range threads {
		if n > 0 && n%backfillProgressEvery == 0 {
			m.editText(s, i, fmt.Sprintf("⏳ Checked %d/%d intro posts…", n, len(threads)))
		}
		m.backfillOne(api, guildID, th, dry, &r)
	}

	summary := backfillSummary(r, days, dry)
	m.editText(s, i, summary)
	if !dry {
		m.modLog(guildID, "📮 Intro Backfill", utils.Colors.Info(),
			logField("Window", plural(days, "day", "days"), true),
			logField("Posted", fmt.Sprint(r.posted), true),
			logField("By", mention(userID(i)), true),
			logField("Skipped", fmt.Sprintf("%d had the button · %d queued or in a squad · %d left the server", r.hasButton, r.inSquads, r.left), false),
			logField("Failed", fmt.Sprint(r.failed), true))
	}
}

func (m *Module) backfillOne(api discordAPI, guildID string, th *forumcache.ThreadMeta, dry bool, r *backfillResult) {
	if _, in, err := m.db.SquadForUser(guildID, th.OwnerID); err != nil {
		r.failed++
		return
	} else if in {
		r.inSquads++
		return
	}
	if _, queued, err := m.db.SquadQueueEntryFor(guildID, th.OwnerID); err != nil {
		r.failed++
		return
	} else if queued {
		r.inSquads++
		return
	}
	if _, err := api.Member(guildID, th.OwnerID); err != nil {
		if discordCode(err) == discordgo.ErrCodeUnknownMember {
			r.left++
		} else {
			r.failed++
		}
		return
	}
	has, err := api.HasJoinPrompt(th.ID)
	switch {
	case err != nil:
		r.failed++
		return
	case has:
		r.hasButton++
		return
	case dry:
		r.posted++
		return
	}
	if _, err := api.Send(th.ID, introPromptMsg(guildID, th.OwnerID, th.ID)); err != nil {
		m.config.Logger.Warnf("squads: backfill join button in intro %s: %v", th.ID, err)
		r.failed++
		return
	}
	r.posted++
	if backfillPause > 0 {
		time.Sleep(backfillPause)
	}
}

// latestIntros keeps each member's newest intro made at or after since,
// oldest first.
func latestIntros(threads []*forumcache.ThreadMeta, since time.Time, botID string) []*forumcache.ThreadMeta {
	latest := map[string]*forumcache.ThreadMeta{}
	for _, th := range threads {
		if th == nil || th.OwnerID == "" || th.OwnerID == botID || th.CreatedAt.Before(since) {
			continue
		}
		if prev := latest[th.OwnerID]; prev == nil || th.CreatedAt.After(prev.CreatedAt) {
			latest[th.OwnerID] = th
		}
	}
	out := make([]*forumcache.ThreadMeta, 0, len(latest))
	for _, th := range latest {
		out = append(out, th)
	}
	sort.Slice(out, func(a, b int) bool {
		if !out[a].CreatedAt.Equal(out[b].CreatedAt) {
			return out[a].CreatedAt.Before(out[b].CreatedAt)
		}
		return out[a].ID < out[b].ID
	})
	return out
}

func backfillSummary(r backfillResult, days int, dry bool) string {
	var b strings.Builder
	verb := "Posted the Join a squad button on"
	if dry {
		verb = "Dry run: would post the Join a squad button on"
	}
	fmt.Fprintf(&b, "✅ %s **%s** from the last %s.\n", verb, plural(r.posted, "intro", "intros"), plural(days, "day", "days"))
	fmt.Fprintf(&b, "-# Checked %s (newest per member). Skipped: %d already had the button, %d queued or in a squad, %d left the server.",
		plural(r.candidates, "post", "posts"), r.hasButton, r.inSquads, r.left)
	if r.failed > 0 {
		fmt.Fprintf(&b, "\n⚠️ %d couldn't be checked or posted to (locked thread or Discord error).", r.failed)
	}
	return b.String()
}
