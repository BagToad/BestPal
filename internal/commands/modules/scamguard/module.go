// Package scamguard implements Layer 1 of the anti-scam image system: a
// perceptual-hash blocklist. On every message create it hashes image
// attachments and, when one is within a configurable Hamming distance of a
// known-bad image, deletes the message, times the author out, and logs to a mod
// channel. Zero AI: hashing is local, free, and deterministic.
//
// Moderators (anyone with the Ban Members permission) are never actioned. The
// known-bad list is seeded from an embedded file and can be grown at runtime
// via the "Mark as Scam Image" message context-menu command, or trimmed via the
// "Unmark Scam Image" command when an image was blocklisted by mistake.
package scamguard

import (
	"sync"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

const (
	// markScamCommandName is the message context-menu command mods use to add a
	// scam image's hash to the blocklist.
	markScamCommandName = "Mark as Scam Image"

	// unmarkScamCommandName is the message context-menu command mods use to
	// remove an image's hash from the blocklist (false-positive recovery).
	unmarkScamCommandName = "Unmark Scam Image"

	// maxImageBytes caps how large an attachment we will download and hash.
	maxImageBytes = 8 * 1024 * 1024

	// maxImageDimension and maxImagePixels bound the *decoded* size of an image.
	// maxImageBytes only limits the compressed download; a small file can decode
	// into an enormous bitmap (a decompression bomb), so we reject images whose
	// declared dimensions exceed these bounds before decoding fully. Scam
	// screenshots are small, and legitimate images well under these limits still
	// hash normally.
	maxImageDimension = 10000
	maxImagePixels    = 24_000_000
)

// Module implements types.CommandModule. It registers one message context-menu
// command and exposes an OnMessageCreate handler (wired in bot.go) that detects
// known scam images.
type Module struct {
	config *config.Config
	db     *database.DB

	mu     sync.RWMutex
	hashes []knownHash // parsed known-bad hashes (in-memory cache)

	// Test seams - overridable so handler logic can be exercised without
	// hitting the network or Discord.
	fetchImage        func(url string, maxBytes int) ([]byte, error)
	deleteMessage     func(s *discordgo.Session, channelID, messageID string) error
	timeoutMember     func(s *discordgo.Session, guildID, userID string, until *time.Time) error
	sendLogEmbed      func(s *discordgo.Session, channelID string, embed *discordgo.MessageEmbed) error
	authorIsModerator func(s *discordgo.Session, e *discordgo.MessageCreate) bool
}

// New creates a new scamguard module and loads the known-bad hash list.
func New(deps *types.Dependencies) *Module {
	m := &Module{
		config: deps.Config,
		db:     deps.DB,
	}
	m.setDefaultSeams()
	m.loadHashes()
	return m
}

// Register registers the "Mark as Scam Image" and "Unmark Scam Image" message
// context-menu commands.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	m.config = deps.Config
	m.db = deps.DB
	m.setDefaultSeams()

	// Gate the commands behind Ban Members - the same permission used to define
	// "moderator" for the detection-skip below, so the people who can mark
	// scams are exactly the people who are exempt from detection.
	var modPerms int64 = discordgo.PermissionBanMembers
	guildOnly := &[]discordgo.InteractionContextType{
		discordgo.InteractionContextGuild,
	}

	cmds[markScamCommandName] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     markScamCommandName,
			Type:                     discordgo.MessageApplicationCommand,
			DefaultMemberPermissions: &modPerms,
			Contexts:                 guildOnly,
		},
		HandlerFunc: m.handleMarkScam,
	}

	cmds[unmarkScamCommandName] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     unmarkScamCommandName,
			Type:                     discordgo.MessageApplicationCommand,
			DefaultMemberPermissions: &modPerms,
			Contexts:                 guildOnly,
		},
		HandlerFunc: m.handleUnmarkScam,
	}
}

// Service returns nil; this module has no recurring service.
func (m *Module) Service() types.ModuleService { return nil }

// setDefaultSeams installs the production implementations for any unset seam.
func (m *Module) setDefaultSeams() {
	if m.fetchImage == nil {
		m.fetchImage = defaultFetchImage
	}
	if m.deleteMessage == nil {
		m.deleteMessage = func(s *discordgo.Session, channelID, messageID string) error {
			return s.ChannelMessageDelete(channelID, messageID)
		}
	}
	if m.timeoutMember == nil {
		m.timeoutMember = func(s *discordgo.Session, guildID, userID string, until *time.Time) error {
			return s.GuildMemberTimeout(guildID, userID, until)
		}
	}
	if m.sendLogEmbed == nil {
		m.sendLogEmbed = func(s *discordgo.Session, channelID string, embed *discordgo.MessageEmbed) error {
			_, err := s.ChannelMessageSendEmbed(channelID, embed)
			return err
		}
	}
	if m.authorIsModerator == nil {
		m.authorIsModerator = defaultAuthorIsModerator
	}
}
