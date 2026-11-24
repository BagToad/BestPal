package squads

import (
	"errors"
	"strings"

	"github.com/bwmarrin/discordgo"
)

// maxSquadName is Discord's limit on a thread name.
const maxSquadName = 100

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
	// Rename outside the lock: Discord rate limits renames, and nothing else
	// should wait on it.
	api := m.dc(nil)
	if err := api.RenameThread(sq.ChannelID, name); err != nil {
		var rl *discordgo.RateLimitError
		switch {
		case errors.As(err, &rl):
			return "⏳ Discord only allows a couple of renames every 10 minutes. Try again in a bit."
		case errors.Is(err, errThreadLocked):
			return "You're not in a squad."
		}
		m.config.Logger.Warnf("squads: rename %s: %v", sq.Name, err)
		return "❌ Could not rename your squad. Try again in a moment."
	}
	lock.Lock()
	err = m.db.RenameSquad(sq.ID, name)
	lock.Unlock()
	if err != nil {
		m.config.Logger.Warnf("squads: save new name of %s: %v", sq.Name, err)
	}
	if _, err := api.Send(sq.ChannelID, &discordgo.MessageSend{
		Content:         "✏️ " + mention(uid) + " renamed the squad to **" + name + "**.",
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("squads: announce rename of %s: %v", name, err)
	}
	return "✏️ Renamed your squad to **" + name + "**."
}
