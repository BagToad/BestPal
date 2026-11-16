package squads

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

type smokeCheck struct {
	name   string
	detail string
}

func (m *Module) handleStackSmokeTest(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondText(s, i, "❌ You need the Ban Members permission to run the smoke test.")
		return
	}
	if m.db == nil || !m.deferReply(s, i) {
		return
	}
	lock := m.guildLock(i.GuildID)
	lock.Lock()
	checks, err := m.runStackSmokeTest(i.GuildID, userID(i))
	lock.Unlock()

	var out strings.Builder
	out.WriteString("## 🧪 Squads stack smoke test\n")
	for _, check := range checks {
		fmt.Fprintf(&out, "✅ **%s** — %s\n", check.name, check.detail)
	}
	if err != nil {
		fmt.Fprintf(&out, "❌ **Failed** — %s\n", err)
		out.WriteString("Cleanup was attempted for every temporary resource.")
	} else {
		out.WriteString("\n**All checks passed.** Temporary Discord and database resources were removed.")
	}
	m.editText(s, i, truncate(out.String(), maxMessageLen))
}

func (m *Module) runStackSmokeTest(guildID, invokerID string) (checks []smokeCheck, resultErr error) {
	api := m.dc(nil)
	botID := api.BotID()
	if invokerID == "" || botID == "" || invokerID == botID {
		return nil, errors.New("could not resolve distinct test members")
	}
	if m.config.ForGuild(guildID).GetSquadsCategoryID() == "" {
		return nil, errors.New("the Squads category is not configured")
	}
	if m.config.ForGuild(guildID).GetGamerPalsModActionLogChannelID() == "" {
		return nil, errors.New("the mod action log is required to verify transcript delivery")
	}
	for _, uid := range []string{invokerID, botID} {
		if _, ok, err := m.db.SquadForUser(guildID, uid); err != nil {
			return nil, fmt.Errorf("check existing squad membership: %w", err)
		} else if ok {
			return nil, fmt.Errorf("%s is already in a squad; leave it before running this test", mention(uid))
		}
		if _, queued, err := m.db.SquadQueueEntryFor(guildID, uid); err != nil {
			return nil, fmt.Errorf("check existing queue membership: %w", err)
		} else if queued {
			return nil, fmt.Errorf("%s is in the squad queue; leave it before running this test", mention(uid))
		}
		if _, err := api.Member(guildID, uid); err != nil {
			return nil, fmt.Errorf("load test member %s: %w", mention(uid), err)
		}
	}

	if allowedSquadTag(" Kids ") || !allowedSquadTag("RPG") {
		return checks, errors.New("sensitive tag filtering failed")
	}
	checks = append(checks, smokeCheck{"Sensitive tags", "excluded Kids while retaining ordinary genres"})

	now := m.now()
	genreA := &candidate{UserID: "smoke-a", Games: map[int64]string{1: "A"}, Tags: map[string]bool{"RPG": true}, TryNew: true, QueuedAt: now}
	genreB := &candidate{UserID: "smoke-b", Games: map[int64]string{2: "B"}, Tags: map[string]bool{"RPG": true}, TryNew: true, QueuedAt: now}
	params := matchParams{Min: 2, Target: 2, Max: 4, FallbackAfter: time.Hour, UnmatchedAfter: 24 * time.Hour}
	if plan := buildPlan([]*candidate{genreA, genreB}, nil, params, now); len(plan.NewSquads) != 1 {
		return checks, errors.New("genre opt-in matching failed")
	}
	genreB.TryNew = false
	if plan := buildPlan([]*candidate{genreA, genreB}, nil, params, now); len(plan.NewSquads) != 0 {
		return checks, errors.New("genre matching ignored opt-in")
	}
	genreB.TryNew = true
	genreA.Avoid = map[string]bool{genreB.UserID: true}
	if plan := buildPlan([]*candidate{genreA, genreB}, nil, params, now); len(plan.NewSquads) != 0 {
		return checks, errors.New("rematch avoidance failed")
	}
	checks = append(checks, smokeCheck{"Matcher", "verified genre opt-in, compatibility, and rematch avoidance"})

	game := database.SquadPick{GameID: 9_999_000_001, Name: "BestPal Stack Smoke Test", Year: 2026, Tags: []string{"RPG", "Kids"}}
	var sq database.Squad
	var err error
	defer func() {
		if sq.ID != 0 {
			if current, ok, _ := m.db.GetSquad(sq.ID); ok {
				_ = m.db.MarkSquadDeletionLogSent(current.ID)
				_ = api.DeleteChannel(current.ChannelID)
				_ = api.DeleteRole(guildID, current.RoleID)
				_ = m.db.DeleteSquad(current.ID, m.now())
				m.unregisterChannel(current.ChannelID)
			}
		}
	}()

	group := []*candidate{
		{UserID: invokerID, Name: "Smoke Test Invoker", Games: map[int64]string{game.GameID: game.Name}, Tags: map[string]bool{"RPG": true}, TryNew: true},
		{UserID: botID, Name: "BestPal", Games: map[int64]string{game.GameID: game.Name}, Tags: map[string]bool{"RPG": true}, TryNew: true},
	}
	sq, err = m.createSquad(guildID, group, false)
	if err != nil {
		return checks, fmt.Errorf("create live squad: %w", err)
	}
	checks = append(checks, smokeCheck{"Formation", "created a real role/channel, welcome post, membership history, and formation DMs"})

	if _, err := api.Send(sq.ChannelID, &discordgo.MessageSend{Content: "BestPal stack smoke-test transcript marker"}); err != nil {
		return checks, fmt.Errorf("write transcript marker: %w", err)
	}
	if status := m.statusText(guildID); !strings.Contains(status, sq.Name) {
		return checks, errors.New("status report omitted the temporary squad")
	}
	if user := m.userText(guildID, invokerID); !strings.Contains(user, "Compatibility") {
		return checks, errors.New("compatibility report was not generated")
	}
	if trends := m.trendsText(guildID); !strings.Contains(trends, "Squad trends") {
		return checks, errors.New("trends report was not generated")
	}
	checks = append(checks, smokeCheck{"Reports", "generated status, compatibility, and popularity/trends reports from live fixtures"})

	future := now.Add(25 * time.Hour)
	reminders := m.sendInactivityRemindersAt(guildID, future, sq.ID)
	if reminders.due == 0 || reminders.dmsAttempted == 0 {
		return checks, errors.New("temporary squad did not exercise inactivity reminder delivery")
	}
	checks = append(checks, smokeCheck{"Inactivity reminders", "ran persisted reminder reconciliation and attempted DM delivery for only the temporary squad"})

	if _, removed, err := m.db.RemoveSquadMember(guildID, botID, database.SquadLeaveVoluntary, future); err != nil || !removed {
		return checks, fmt.Errorf("record ghosting departure: %w", err)
	}
	history, err := m.db.SquadUserHistoryStats(guildID, botID)
	if err != nil {
		return checks, fmt.Errorf("load ghosting history: %w", err)
	}
	if history.Ghosting == 0 {
		return checks, errors.New("ghosting history omitted the fixture departure")
	}
	if ghosters := m.ghostersText(guildID); !strings.Contains(ghosters, "Top squad ghosters") {
		return checks, errors.New("ghosting report was not generated")
	}
	checks = append(checks, smokeCheck{"Ghosting analytics", "recorded and reported a zero-message voluntary departure"})

	sq.Members = 1
	if err := m.deleteSquad(sq, "it dropped below 2 members"); err != nil {
		return checks, fmt.Errorf("delete live squad with transcript: %w", err)
	}
	if _, ok, err := m.db.GetSquad(sq.ID); err != nil || ok {
		return checks, errors.New("temporary squad survived deletion")
	}
	partners, err := m.db.PreviousSquadPartners(guildID, invokerID)
	if err != nil || !partners[botID] {
		return checks, errors.New("completed membership history did not preserve rematch avoidance")
	}
	checks = append(checks, smokeCheck{"Dissolution", "sent deletion DM, captured/delivered transcript, removed resources, and preserved rematch history"})
	sq = database.Squad{}
	return checks, nil
}
