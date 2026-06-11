package scamguard

import (
	"github.com/bwmarrin/discordgo"
)

// OnMessageCreate hashes image attachments and enforces on a known-bad match.
// It is wired in bot.go via session.AddHandler. All work is skipped unless the
// module is enabled.
func (m *Module) OnMessageCreate(s *discordgo.Session, e *discordgo.MessageCreate) {
	if m == nil || m.config == nil || !m.config.GetScamGuardEnabled() {
		return
	}
	if e.Author == nil || e.Author.Bot {
		return
	}
	if e.GuildID == "" || len(e.Attachments) == 0 {
		return
	}
	// Never action moderators (anyone who can ban members).
	if m.authorIsModerator(s, e) {
		return
	}

	threshold := m.config.GetScamGuardHashThreshold()
	for _, a := range e.Attachments {
		if a == nil || !isImageAttachment(a) {
			continue
		}
		if a.Size > maxImageBytes {
			continue
		}
		data, err := m.fetchImage(a.URL, maxImageBytes)
		if err != nil {
			m.config.Logger.Debugf("scamguard: failed to fetch %q: %v", a.URL, err)
			continue
		}
		h, err := computeHash(data)
		if err != nil {
			m.config.Logger.Debugf("scamguard: failed to hash %q: %v", a.URL, err)
			continue
		}
		if matched, ok := m.matchHash(h, threshold); ok {
			m.enforce(s, e, a, matched)
			return
		}
	}
}

// defaultAuthorIsModerator reports whether the message author has the Ban
// Members or Administrator permission (or is the guild owner). Ban Members and
// Administrator are guild-level permissions that cannot be granted by per-
// channel overwrites, so role permissions alone are authoritative.
//
// It resolves from cached guild roles plus the member object Discord includes
// on guild MESSAGE_CREATE events, so the common path makes no API call. If that
// is unavailable it falls back to a permission lookup (which may hit the REST
// API). When moderator status genuinely cannot be determined it fails closed
// (treats the author as a moderator) so a real moderator is never actioned
// because of a transient lookup failure.
func defaultAuthorIsModerator(s *discordgo.Session, e *discordgo.MessageCreate) bool {
	if s == nil || e == nil || e.Author == nil {
		return false
	}
	const modBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator

	if s.State != nil {
		if g, err := s.State.Guild(e.GuildID); err == nil {
			if g.OwnerID == e.Author.ID {
				return true
			}
			if e.Member != nil {
				return rolesGrantModerator(g, e.Member.Roles, modBits)
			}
			if mem, err := s.State.Member(e.GuildID, e.Author.ID); err == nil {
				return rolesGrantModerator(g, mem.Roles, modBits)
			}
		}
	}

	// Fallback: guild not cached and no member on the event. May hit the REST
	// API. Fail closed when we still can't tell.
	perms, err := s.UserChannelPermissions(e.Author.ID, e.ChannelID)
	if err != nil {
		return true
	}
	return perms&modBits != 0
}

// rolesGrantModerator reports whether the @everyone role or any of memberRoles
// grants one of modBits.
func rolesGrantModerator(g *discordgo.Guild, memberRoles []string, modBits int64) bool {
	var perms int64
	for _, r := range g.Roles {
		if r.ID == g.ID { // @everyone role shares the guild ID
			perms |= r.Permissions
			break
		}
	}
	for _, r := range g.Roles {
		for _, mr := range memberRoles {
			if r.ID == mr {
				perms |= r.Permissions
				break
			}
		}
	}
	return perms&modBits != 0
}
