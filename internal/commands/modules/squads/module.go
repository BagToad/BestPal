// Package squads groups new pals into small squads. Members pick the games
// they play from an IGDB search, wait in a queue, and a matcher groups people
// with the same region and age group who share games (or at least genres).
// Each squad gets a role and a channel, which is deleted once it goes quiet.
package squads

import (
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

const (
	prefix     = "sq:"
	squadColor = 0x57F287

	// maxPicks is how many games someone can match on (a select menu's limit).
	maxPicks = 25
)

// Module implements squads.
type Module struct {
	config  *config.Config
	db      *database.DB
	games   gameSource
	api     discordAPI // overrides the session's API in tests
	session *discordgo.Session
	service *Service
	now     func() time.Time
	randN   func(n int) int

	mu    sync.Mutex
	locks map[string]*sync.Mutex

	chMu     sync.Mutex
	channels map[string]int64    // squad channel ID -> squad ID
	touched  map[int64]time.Time // last recorded activity per squad

	gameCache sync.Map // int64 -> gameInfo from recent searches

	// introThread finds a member's latest intro thread ("" if unknown).
	introThread func(guildID, userID string) string
}

// New creates the module.
func New(deps *types.Dependencies) *Module {
	m := &Module{
		config:   deps.Config,
		db:       deps.DB,
		session:  deps.Session,
		now:      time.Now,
		randN:    rand.IntN,
		locks:    map[string]*sync.Mutex{},
		channels: map[string]int64{},
		touched:  map[int64]time.Time{},
	}
	if deps.IGDBClient != nil {
		m.games = &igdbSource{client: deps.IGDBClient}
	}
	if fc := deps.ForumCache; fc != nil {
		m.introThread = func(guildID, userID string) string {
			forum := m.config.ForGuild(guildID).GetGamerPalsIntroductionsForumChannelID()
			if forum == "" {
				return ""
			}
			if meta, ok := fc.GetLatestUserThread(forum, userID); ok && meta != nil {
				return meta.ID
			}
			return ""
		}
	}
	m.service = &Service{m: m}
	return m
}

// Service returns the scheduler-backed service.
func (m *Module) Service() types.ModuleService { return m.service }

// dc returns the Discord API to use: the test fake, else the given or
// hydrated session.
func (m *Module) dc(s *discordgo.Session) discordAPI {
	if m.api != nil {
		return m.api
	}
	if s == nil {
		s = m.session
	}
	return sessionAPI{s: s}
}

func (m *Module) guildLock(guildID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[guildID]
	if !ok {
		l = &sync.Mutex{}
		m.locks[guildID] = l
	}
	return l
}

// Register adds /squad and /squad-admin.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	guildOnly := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	cmds["squad"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "squad",
			Description: "Squads: small groups of new pals matched on the games you play",
			Contexts:    guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "join", Description: "Pick your games and join the squad queue"},
				{Type: discordgo.ApplicationCommandOptionSubCommand, Name: "leave", Description: "Leave your squad (or the queue)"},
			},
		},
		HandlerFunc: m.handleSquad,
	}

	banPerms := int64(discordgo.PermissionBanMembers)
	userOpt := func(desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionUser, Name: "user", Description: desc, Required: true}
	}
	squadOpt := &discordgo.ApplicationCommandOption{
		Type: discordgo.ApplicationCommandOptionString, Name: "squad", Description: "Squad", Required: true, Autocomplete: true,
	}
	sub := func(name, desc string, opts ...*discordgo.ApplicationCommandOption) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: name, Description: desc, Options: opts}
	}
	cmds["squad-admin"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "squad-admin",
			Description:              "Squad moderation and troubleshooting",
			DefaultMemberPermissions: &banPerms,
			Contexts:                 guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				sub("status", "Queue size, next pairing and live squads"),
				sub("dryrun", "Show what pairing would do right now, without changing anything"),
				sub("run-now", "Run pairing right now"),
				sub("user", "Show someone's games, queue and squad", userOpt("Member")),
				sub("add", "Add someone to a squad (ignores size limits)", userOpt("Member to add"), squadOpt),
				sub("remove", "Remove someone from their squad", userOpt("Member to remove")),
				sub("delete", "Delete a squad's channel and role", squadOpt),
				sub("queue-remove", "Take someone out of the queue", userOpt("Member")),
				sub("refresh-permissions", "Re-apply channel permissions to every squad (after changing visibility)"),
			},
		},
		HandlerFunc: m.handleAdmin,
	}
}

// canManage mirrors /config access: super admins, Ban Members or Administrator.
func (m *Module) canManage(i *discordgo.InteractionCreate) bool {
	if id := userID(i); id != "" && utils.IsSuperAdmin(id, m.config) {
		return true
	}
	if i.Member == nil {
		return false
	}
	const modBits = discordgo.PermissionBanMembers | discordgo.PermissionAdministrator
	return i.Member.Permissions&modBits != 0
}

func userID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func ephemeral(content string) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		Content:         content,
		Flags:           discordgo.MessageFlagsEphemeral,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
}

func (m *Module) respond(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource, Data: data,
	}); err != nil {
		m.config.Logger.Warnf("squads: respond: %v", err)
	}
}

func (m *Module) respondText(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	m.respond(s, i, ephemeral(content))
}

func (m *Module) update(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage, Data: data,
	}); err != nil {
		m.config.Logger.Warnf("squads: update: %v", err)
	}
}

// deferUpdate acknowledges a component so a slow action can edit it later.
func (m *Module) deferUpdate(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		m.config.Logger.Warnf("squads: defer: %v", err)
		return false
	}
	return true
}

func (m *Module) deferReply(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		m.config.Logger.Warnf("squads: defer: %v", err)
		return false
	}
	return true
}

// edit replaces a deferred response.
func (m *Module) edit(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	comps := data.Components
	if comps == nil {
		comps = []discordgo.MessageComponent{}
	}
	content := data.Content
	if err := m.dc(s).EditResponse(i.Interaction, &discordgo.WebhookEdit{
		Content:         &content,
		Components:      &comps,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("squads: edit response: %v", err)
	}
}

// lookupIntro returns a member's intro thread, if it can be found.
func (m *Module) lookupIntro(guildID, userID string) string {
	if m.introThread == nil {
		return ""
	}
	return m.introThread(guildID, userID)
}

func (m *Module) editText(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	m.edit(s, i, &discordgo.InteractionResponseData{Content: content})
}

// HandleComponent routes sq: buttons and selects.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	parts := strings.Split(strings.TrimPrefix(i.MessageComponentData().CustomID, prefix), ":")
	arg := func(n int) string {
		if n < len(parts) {
			return parts[n]
		}
		return ""
	}
	switch parts[0] {
	case "join":
		m.handleJoinButton(s, i, arg(1), arg(2), arg(3))
	case "add":
		m.handleAddButton(s, i, pickerCtx{GuildID: arg(1), ThreadID: arg(2)})
	case "res":
		m.handleResultPick(s, i, pickerCtx{GuildID: arg(1), ThreadID: arg(2)})
	case "rm":
		m.handleRemovePick(s, i, pickerCtx{GuildID: arg(1), ThreadID: arg(2)})
	case "back":
		m.handleBack(s, i, pickerCtx{GuildID: arg(1), ThreadID: arg(2)})
	case "done":
		m.handleDone(s, i, pickerCtx{GuildID: arg(1), ThreadID: arg(2)})
	case "leave":
		m.handleLeaveButton(s, i, arg(1))
	case "leaveyes":
		m.handleLeaveConfirm(s, i, arg(1))
	case "adel":
		m.handleAdminDeleteConfirm(s, i, arg(1))
	default:
		m.respondText(s, i, "❌ Unknown squad action.")
	}
}

// HandleModalSubmit routes sq: modals.
func (m *Module) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	parts := strings.Split(strings.TrimPrefix(i.ModalSubmitData().CustomID, prefix), ":")
	if parts[0] == "search" && len(parts) >= 3 {
		m.handleSearchSubmit(s, i, pickerCtx{GuildID: parts[1], ThreadID: parts[2]})
		return
	}
	m.respondText(s, i, "❌ Unknown squad form.")
}

func modalValue(i *discordgo.InteractionCreate, id string) string {
	for _, comp := range i.ModalSubmitData().Components {
		var row *discordgo.ActionsRow
		switch v := comp.(type) {
		case discordgo.ActionsRow:
			row = &v
		case *discordgo.ActionsRow:
			row = v
		default:
			continue
		}
		for _, inner := range row.Components {
			if ti, ok := inner.(*discordgo.TextInput); ok && ti.CustomID == id {
				return ti.Value
			}
		}
	}
	return ""
}

func parseID(s string) (int64, bool) {
	n, err := strconv.ParseInt(s, 10, 64)
	return n, err == nil && n > 0
}

// modLog posts an admin action to the mod action log, if one is configured.
func (m *Module) modLog(guildID, text string) {
	ch := m.config.ForGuild(guildID).GetGamerPalsModActionLogChannelID()
	if ch == "" {
		return
	}
	if _, err := m.dc(nil).Send(ch, &discordgo.MessageSend{
		Content:         "🧑‍🤝‍🧑 **Squads:** " + text,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}); err != nil {
		m.config.Logger.Warnf("squads: mod log: %v", err)
	}
}

func mention(uid string) string { return "<@" + uid + ">" }

func plural(n int, one, many string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, one)
	}
	return fmt.Sprintf("%d %s", n, many)
}
