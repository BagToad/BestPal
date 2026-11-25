// Package feud runs GP Feud, a Family Feud style game whose answers come from
// the server. Members submit questions (mods approve them) and answer them
// anonymously; once enough people have answered, a question can be played.
package feud

import (
	"context"
	"errors"
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
	copilot "github.com/github/copilot-sdk/go"
)

const (
	prefix    = "gf:"
	feudColor = 0xF1C40F
)

// Module implements GP Feud.
type Module struct {
	config  *config.Config
	db      *database.DB
	session *discordgo.Session
	api     discordAPI // overrides the session's API in tests
	now     func() time.Time
	randN   func(n int) int

	seeded sync.Map // guild ID -> struct{} once starter questions are in

	agent     completer
	afterFunc func(d time.Duration, f func()) (stop func())
	spawn     func(f func())

	mu     sync.Mutex
	locks  map[string]*chanMutex
	timers map[string]func()

	tidying map[string]bool // running agent tidy-ups, guarded by mu
}

// completer runs one-shot agent tasks (agentengine.Agent).
type completer interface {
	Complete(ctx context.Context, system, prompt string, tools []copilot.Tool) (string, error)
}

// SetAgent lets GP Feud ask the agent about close-call guesses.
func (m *Module) SetAgent(a completer) { m.agent = a }

// New creates the module.
func New(deps *types.Dependencies) *Module {
	return &Module{
		config:  deps.Config,
		db:      deps.DB,
		session: deps.Session,
		now:     time.Now,
		randN:   rand.IntN,
		afterFunc: func(d time.Duration, f func()) func() {
			t := time.AfterFunc(d, f)
			return func() { t.Stop() }
		},
		spawn:  func(f func()) { go f() },
		locks:  map[string]*chanMutex{},
		timers: map[string]func(){},
	}
}

// Service restores running games after a restart and sweeps for missed
// timers.
func (m *Module) Service() types.ModuleService { return service{m} }

type service struct{ m *Module }

func (s service) HydrateServiceDiscordSession(*discordgo.Session) error {
	s.m.Recover()
	return nil
}

func (s service) ScheduledFuncs() map[string]func() error {
	if s.m.db == nil {
		return nil
	}
	return map[string]func() error{"@every 1m": s.m.sweep}
}

// discordAPI is the slice of the Discord API the module uses.
type discordAPI interface {
	Respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error
	Send(channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error)
	Edit(msg *discordgo.MessageEdit) (*discordgo.Message, error)
	CreateThread(parentID, name string) (*discordgo.Channel, error)
	AddThreadMember(threadID, userID string) error
	RemoveThreadMember(threadID, userID string) error
	EditResponse(i *discordgo.Interaction, e *discordgo.WebhookEdit) error
}

type sessionAPI struct{ s *discordgo.Session }

func (a sessionAPI) Respond(i *discordgo.Interaction, r *discordgo.InteractionResponse) error {
	return a.s.InteractionRespond(i, r)
}

func (a sessionAPI) EditResponse(i *discordgo.Interaction, e *discordgo.WebhookEdit) error {
	_, err := a.s.InteractionResponseEdit(i, e)
	return err
}

func (a sessionAPI) Send(channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
	return a.s.ChannelMessageSendComplex(channelID, msg)
}

func (a sessionAPI) Edit(msg *discordgo.MessageEdit) (*discordgo.Message, error) {
	return a.s.ChannelMessageEditComplex(msg)
}

// teamThreadArchive keeps quiet team threads open for a week, the most
// Discord allows; posting reopens them.
const teamThreadArchive = 10080

func (a sessionAPI) CreateThread(parentID, name string) (*discordgo.Channel, error) {
	return a.s.ThreadStartComplex(parentID, &discordgo.ThreadStart{
		Name: name, AutoArchiveDuration: teamThreadArchive,
		Type: discordgo.ChannelTypeGuildPrivateThread, Invitable: false,
	})
}

func (a sessionAPI) AddThreadMember(threadID, userID string) error {
	return a.onOpenThread(threadID, func() error { return a.s.ThreadMemberAdd(threadID, userID) })
}

func (a sessionAPI) RemoveThreadMember(threadID, userID string) error {
	return a.onOpenThread(threadID, func() error { return a.s.ThreadMemberRemove(threadID, userID) })
}

// onOpenThread retries op once after unarchiving the thread.
func (a sessionAPI) onOpenThread(threadID string, op func() error) error {
	err := op()
	var rest *discordgo.RESTError
	if !errors.As(err, &rest) || rest.Message == nil || rest.Message.Code != discordgo.ErrCodePerformedOperationOnArchivedThread {
		return err
	}
	if _, err := a.s.ChannelEdit(threadID, &discordgo.ChannelEdit{Archived: new(false)}); err != nil {
		return err
	}
	return op()
}

func (m *Module) dc(s *discordgo.Session) discordAPI {
	if m.api != nil {
		return m.api
	}
	if s == nil {
		s = m.session
	}
	return sessionAPI{s: s}
}

// Register adds /feud and /feud-admin.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	guildOnly := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	sub := func(name, desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{Type: discordgo.ApplicationCommandOptionSubCommand, Name: name, Description: desc}
	}
	cmds["feud"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "feud",
			Description: "GP Feud: a Family Feud style game with answers from the server",
			Contexts:    guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				sub("answer", "Answer survey questions for future games"),
				sub("submit", "Suggest a survey question"),
			},
		},
		HandlerFunc: m.handleFeud,
	}
	banPerms := int64(discordgo.PermissionBanMembers)
	cmds["feud-admin"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "feud-admin",
			Description:              "GP Feud moderation",
			DefaultMemberPermissions: &banPerms,
			Contexts:                 guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				sub("review", "Approve or reject submitted questions"),
				sub("edit", "Edit questions and their answers"),
				sub("dedupe", "Have the agent merge duplicate answers and hide junk on the ready questions"),
				sub("pool", "Show how many questions are ready, collecting answers, or awaiting review"),
				sub("setup", "Post the game board and create the team threads in the GP Feud channel"),
				sub("start", "Start a game"),
				sub("end", "End the current game"),
			},
		},
		HandlerFunc: m.handleAdmin,
	}
}

func (m *Module) handleFeud(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	if !m.open(s, i) {
		return
	}
	switch subcommand(i) {
	case "answer":
		m.handleAnswerButton(s, i)
	case "submit":
		m.openSubmitModal(s, i)
	}
}

func (m *Module) handleAdmin(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	if !m.canManage(i) {
		m.respondText(s, i, "❌ Only moderators can do that.")
		return
	}
	switch subcommand(i) {
	case "review":
		m.handleReviewNext(s, i, "", false)
	case "edit":
		m.handleEditCommand(s, i)
	case "dedupe":
		m.handleDedupe(s, i)
	case "pool":
		m.handlePool(s, i)
	case "setup":
		m.handleSetup(s, i)
	case "start":
		if m.open(s, i) {
			m.handleStart(s, i)
		}
	case "end":
		m.handleEnd(s, i)
	}
}

// HandleComponent routes gf: buttons.
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
	case "answer":
		if m.open(s, i) {
			m.handleAnswerButton(s, i)
		}
	case "submit":
		if m.open(s, i) {
			m.openSubmitModal(s, i)
		}
	case "ed":
		m.handleEdit(s, i, parts[1:])
	case "approve", "reject":
		m.handleReview(s, i, arg(1), parts[0] == "approve", arg(2) == "next")
	case "join":
		if m.open(s, i) {
			m.handleJoin(s, i)
		}
	case "leave":
		m.handleLeave(s, i)
	case "suggest":
		m.openSuggestModal(s, i, arg(1))
	case "vote":
		m.handleVote(s, i, arg(1), arg(2))
	}
}

// HandleModalSubmit routes gf: modals.
func (m *Module) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.db == nil {
		m.respondText(s, i, "❌ Database is unavailable.")
		return
	}
	parts := strings.Split(strings.TrimPrefix(i.ModalSubmitData().CustomID, prefix), ":")
	if !m.open(s, i) {
		return
	}
	switch parts[0] {
	case "ans":
		if len(parts) > 1 {
			m.handleAnswerSubmit(s, i, parts[1])
		}
	case "sub":
		m.handleSubmitQuestion(s, i)
	case "sg":
		if len(parts) > 1 {
			m.handleSuggest(s, i, parts[1])
		}
	case "edq", "eda", "edr", "edb":
		if id, err := strconv.ParseInt(strings.Join(parts[1:], ""), 10, 64); err == nil {
			m.handleEditModal(s, i, parts[0], id)
		}
	}
}

// open reports whether GP Feud is on, telling the user if not.
func (m *Module) open(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	if i.GuildID == "" || !m.config.ForGuild(i.GuildID).GetFeudEnabled() {
		m.respondText(s, i, "GP Feud isn't running right now.")
		return false
	}
	return true
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

func subcommand(i *discordgo.InteractionCreate) string {
	opts := i.ApplicationCommandData().Options
	if len(opts) == 0 {
		return ""
	}
	return opts[0].Name
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

func ephemeral(content string, comps ...discordgo.MessageComponent) *discordgo.InteractionResponseData {
	return &discordgo.InteractionResponseData{
		Content:         content,
		Components:      comps,
		Flags:           discordgo.MessageFlagsEphemeral,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
}

func (m *Module) respond(s *discordgo.Session, i *discordgo.InteractionCreate, typ discordgo.InteractionResponseType, data *discordgo.InteractionResponseData) {
	if err := m.dc(s).Respond(i.Interaction, &discordgo.InteractionResponse{Type: typ, Data: data}); err != nil {
		m.config.Logger.Warnf("feud: respond: %v", err)
	}
}

func (m *Module) respondText(s *discordgo.Session, i *discordgo.InteractionCreate, content string, comps ...discordgo.MessageComponent) {
	m.respond(s, i, discordgo.InteractionResponseChannelMessageWithSource, ephemeral(content, comps...))
}

func modalValue(i *discordgo.InteractionCreate, id string) string {
	var find func(comps []discordgo.MessageComponent) (string, bool)
	find = func(comps []discordgo.MessageComponent) (string, bool) {
		for _, comp := range comps {
			switch v := comp.(type) {
			case *discordgo.TextInput:
				if v.CustomID == id {
					return v.Value, true
				}
			case discordgo.TextInput:
				if v.CustomID == id {
					return v.Value, true
				}
			case discordgo.ActionsRow:
				if val, ok := find(v.Components); ok {
					return val, true
				}
			case *discordgo.ActionsRow:
				if val, ok := find(v.Components); ok {
					return val, true
				}
			case discordgo.Label:
				if val, ok := find([]discordgo.MessageComponent{v.Component}); ok {
					return val, true
				}
			case *discordgo.Label:
				if val, ok := find([]discordgo.MessageComponent{v.Component}); ok {
					return val, true
				}
			}
		}
		return "", false
	}
	v, _ := find(i.ModalSubmitData().Components)
	return v
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

func button(label, emoji string, style discordgo.ButtonStyle, id string) discordgo.Button {
	b := discordgo.Button{Label: label, Style: style, CustomID: id}
	if emoji != "" {
		b.Emoji = &discordgo.ComponentEmoji{Name: emoji}
	}
	return b
}
