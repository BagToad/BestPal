package squads

import (
	"errors"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// maxSquadName is Discord's limit on a thread name.
const maxSquadName = 100

// Discord allows two renames of a channel or thread every 10 minutes. The bot
// tracks them itself so a third is refused up front: once Discord's bucket is
// empty, every later edit of the thread (including closing it) would wait for
// the bucket to refill.
const (
	renameLimit  = 2
	renameWindow = 10 * time.Minute
)

// reserveRename claims a rename of a squad's thread. It fails while another
// rename of the same squad is in flight (busy) or the squad has used up its
// renames (limited). A successful claim must be released with finishRename.
func (m *Module) reserveRename(squadID int64) (busy, limited bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.renaming[squadID] {
		return true, false
	}
	recent := m.renames[squadID][:0]
	for _, at := range m.renames[squadID] {
		if now.Sub(at) < renameWindow {
			recent = append(recent, at)
		}
	}
	if m.renames == nil {
		m.renames = map[int64][]time.Time{}
		m.renaming = map[int64]bool{}
	}
	m.renames[squadID] = recent
	if len(recent) >= renameLimit {
		return false, true
	}
	m.renaming[squadID] = true
	return false, false
}

// finishRename releases a claim from reserveRename, counting it toward the
// limit if Discord saw the rename.
func (m *Module) finishRename(squadID int64, counted bool) {
	now := m.now()
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.renaming, squadID)
	if counted {
		m.renames[squadID] = append(m.renames[squadID], now)
	}
}

// renameSquad renames the thread of the squad uid is in and returns the reply
// for them. Legacy channel squads can't be renamed.
func (m *Module) renameSquad(guildID, uid, name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		return "❌ Give your squad a name."
	}
	if n := []rune(name); len(n) > maxSquadName {
		name = string(n[:maxSquadName])
	}
	lock := m.guildLock(guildID)
	lock.Lock()
	sq, ok, err := m.db.SquadForUser(guildID, uid)
	lock.Unlock()
	switch {
	case err != nil:
		return "❌ Could not load your squad. Try again in a moment."
	case !ok || sq.Deleting():
		return "You're not in a squad."
	case !sq.Thread:
		return "Renaming isn't available for squads that have their own channel. Newer squads live in private threads, and those can be renamed."
	case name == sq.Name:
		return "Your squad is already called **" + name + "**."
	}
	busy, limited := m.reserveRename(sq.ID)
	switch {
	case busy:
		return "⏳ Someone else is renaming your squad right now. Try again in a moment."
	case limited:
		return "⏳ Discord only allows a couple of renames every 10 minutes. Try again in a bit."
	}
	// Rename outside the lock: Discord rate limits renames, and nothing else
	// should wait on it.
	api := m.dc(nil)
	if err := api.RenameThread(sq.ChannelID, name); err != nil {
		var rl *discordgo.RateLimitError
		limited := errors.As(err, &rl)
		m.finishRename(sq.ID, limited)
		switch {
		case limited:
			return "⏳ Discord only allows a couple of renames every 10 minutes. Try again in a bit."
		case errors.Is(err, errThreadLocked):
			return "You're not in a squad."
		}
		m.config.Logger.Warnf("squads: rename %s: %v", sq.Name, err)
		return "❌ Could not rename your squad. Try again in a moment."
	}
	m.finishRename(sq.ID, true)
	// The squad may have closed while Discord renamed it.
	lock.Lock()
	cur, found, err := m.db.GetSquad(sq.ID)
	if err == nil && found && !cur.Deleting() {
		if err = m.db.RenameSquad(sq.ID, name); err != nil {
			err = m.db.RenameSquad(sq.ID, name)
		}
	}
	lock.Unlock()
	switch {
	case err != nil:
		m.config.Logger.Warnf("squads: save new name of %s: %v", sq.Name, err)
	case !found || cur.Deleting():
		return "You're not in a squad."
	}
	if _, err := api.Send(sq.ChannelID, &discordgo.MessageSend{
		Content:         "✏️ " + mention(uid) + " renamed the squad to **" + name + "**.",
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("squads: announce rename of %s: %v", name, err)
	}
	return "✏️ Renamed your squad to **" + name + "**."
}
