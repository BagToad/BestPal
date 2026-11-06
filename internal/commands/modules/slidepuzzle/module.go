// Package slidepuzzle runs a community sliding-tile puzzle: one board message
// per guild, members vote on which numbered tile to slide into the gap, and
// every round the most-voted move is made until the picture is complete.
package slidepuzzle

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
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

//go:embed assets/silly_horse.jpg
var defaultImage []byte

const (
	prefix          = "sp:"
	voteButtonID    = prefix + "vote"
	helpButtonID    = prefix + "help"
	pickPrefix      = prefix + "pick:"
	resetButtonID   = prefix + "reset"
	resetConfirmID  = prefix + "reset-confirm"
	uploadButtonID  = prefix + "upload"
	uploadModalID   = prefix + "upload-modal"
	uploadFileID    = prefix + "upload-file"
	boardFile       = "puzzle.jpg"
	puzzleColor     = 0x8B5A2B
	solvedColor     = 0x57F287
	maxUploadBytes  = 10 << 20
	downloadTimeout = 20 * time.Second
)

// store is the persistence the module needs; *database.DB satisfies it.
type store interface {
	GetSlidePuzzle(guildID string) (database.SlidePuzzle, bool, error)
	StartSlidePuzzle(p database.SlidePuzzle, now time.Time) (database.SlidePuzzle, error)
	MarkSlidePuzzlePublished(guildID string, round int64) error
	MarkSlidePuzzleRefreshed(guildID string, round int64) error
	CastSlidePuzzleVote(guildID string, round int64, userID string, tile int, now time.Time) (int, bool, error)
	SlidePuzzleUserVote(guildID string, round int64, userID string) (int, bool, error)
	SlidePuzzleTally(guildID string, round int64) (map[int]int, error)
	AdvanceSlidePuzzle(guildID string, round int64, board []int, solved bool, roundEnds, now time.Time) error
	ExtendSlidePuzzleRound(guildID string, round int64, roundEnds time.Time) error
	DueSlidePuzzles(now time.Time) ([]database.SlidePuzzle, error)
	AddSlidePuzzleImage(guildID, name string, data []byte, addedBy string, now time.Time) (int64, error)
	SlidePuzzleImageIDs(guildID string) ([]int64, error)
	SetSlidePuzzleMessage(guildID, channelID, messageID string) error
	GetSlidePuzzleImage(guildID string, id int64) ([]byte, bool, error)
}

// discordOps wraps the Discord and HTTP calls the module makes so tests can
// capture them.
type discordOps struct {
	Respond       func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error
	EditResponse  func(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error
	SendMessage   func(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error)
	EditMessage   func(s *discordgo.Session, edit *discordgo.MessageEdit) error
	DeleteMessage func(s *discordgo.Session, channelID, messageID string) error
	Download      func(url string, limit int64) ([]byte, error)
}

func defaultDiscordOps() discordOps {
	client := &http.Client{Timeout: downloadTimeout}
	return discordOps{
		Respond: func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
			return s.InteractionRespond(i, resp)
		},
		EditResponse: func(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
			_, err := s.InteractionResponseEdit(i, edit)
			return err
		},
		SendMessage: func(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error) {
			return s.ChannelMessageSendComplex(channelID, msg)
		},
		EditMessage: func(s *discordgo.Session, edit *discordgo.MessageEdit) error {
			_, err := s.ChannelMessageEditComplex(edit)
			return err
		},
		DeleteMessage: func(s *discordgo.Session, channelID, messageID string) error {
			return s.ChannelMessageDelete(channelID, messageID)
		},
		Download: func(url string, limit int64) ([]byte, error) {
			resp, err := client.Get(url)
			if err != nil {
				return nil, err
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return nil, fmt.Errorf("download failed: HTTP %d", resp.StatusCode)
			}
			data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
			if err != nil {
				return nil, err
			}
			if int64(len(data)) > limit {
				return nil, fmt.Errorf("file is larger than %d MB", limit>>20)
			}
			return data, nil
		},
	}
}

// Module implements the slide puzzle.
type Module struct {
	config  *config.Config
	store   store
	ops     discordOps
	session *discordgo.Session
	service *Service
	now     func() time.Time
	randN   func(n int) int

	mu    sync.Mutex
	locks map[string]*sync.Mutex

	// afterFunc schedules a round's resolution at its exact end time; the
	// per-minute tick is only a fallback.
	afterFunc func(d time.Duration, f func()) *time.Timer
	timerMu   sync.Mutex
	timers    map[string]*time.Timer
	redraws   map[string]*time.Timer
}

// New creates the module.
func New(deps *types.Dependencies) *Module {
	m := &Module{
		config:  deps.Config,
		ops:     defaultDiscordOps(),
		session: deps.Session,
		now:     time.Now,
		randN:   rand.IntN,
		locks:   map[string]*sync.Mutex{},

		afterFunc: time.AfterFunc,
		timers:    map[string]*time.Timer{},
		redraws:   map[string]*time.Timer{},
	}
	if deps.DB != nil {
		m.store = deps.DB
	}
	m.service = &Service{m: m}
	return m
}

// Service returns the module's scheduler-backed service.
func (m *Module) Service() types.ModuleService { return m.service }

// ConfigSettings declares the puzzle's per-guild settings.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeySlidePuzzleGridSize,
			Category:    config.CategorySlidePuzzle,
			Label:       "Grid size",
			Description: fmt.Sprintf("Tiles per side (%d–%d). Applies when the puzzle is set up or reset.", config.SlidePuzzleMinGrid, config.SlidePuzzleMaxGrid),
			Kind:        config.KindInt,
			Default:     5,
			Validate:    validateGrid,
		},
		{
			Key:         config.KeySlidePuzzleRoundDuration,
			Category:    config.CategorySlidePuzzle,
			Label:       "Voting round length",
			Description: "How long each round of voting lasts before the top move is made (e.g. 8h). Applies from the next round.",
			Kind:        config.KindDuration,
			Default:     "8h",
			Validate:    validateRound,
		},
	}
}

// ConfigActions adds the reset and upload buttons to the Slide Puzzle panel.
func (m *Module) ConfigActions() []config.Action {
	return []config.Action{
		{Category: config.CategorySlidePuzzle, Label: "🔄 Reset puzzle", CustomID: resetButtonID},
		{Category: config.CategorySlidePuzzle, Label: "🖼️ Upload image", CustomID: uploadButtonID},
	}
}

func validateGrid(v string) error {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("must be a whole number")
	}
	if n < config.SlidePuzzleMinGrid || n > config.SlidePuzzleMaxGrid {
		return fmt.Errorf("must be between %d and %d", config.SlidePuzzleMinGrid, config.SlidePuzzleMaxGrid)
	}
	return nil
}

func validateRound(v string) error {
	d, err := time.ParseDuration(strings.TrimSpace(v))
	if err != nil {
		return fmt.Errorf("must be a duration like 8h or 30m")
	}
	if d < config.SlidePuzzleMinRound {
		return fmt.Errorf("must be at least %s", config.SlidePuzzleMinRound)
	}
	return nil
}

// Register adds /slidepuzzle-setup.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	banPerms := int64(discordgo.PermissionBanMembers)
	cmds["slidepuzzle-setup"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "slidepuzzle-setup",
			Description:              "Start the community slide puzzle in this channel (moves it here if it's elsewhere)",
			DefaultMemberPermissions: &banPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleSetup,
	}
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

func (m *Module) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:         content,
			Flags:           discordgo.MessageFlagsEphemeral,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	})
}

func (m *Module) deferEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate) error {
	return m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	})
}

func (m *Module) editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := m.ops.EditResponse(s, i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		m.config.Logger.Warnf("slide puzzle: edit response: %v", err)
	}
}

// handleSetup starts a fresh puzzle in the invoking channel.
func (m *Module) handleSetup(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondEphemeral(s, i, "❌ You need the Ban Members permission to set up the puzzle.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	if i.GuildID == "" {
		m.respondEphemeral(s, i, "❌ Run this in a server channel.")
		return
	}
	if err := m.deferEphemeral(s, i); err != nil {
		m.config.Logger.Warnf("slide puzzle: defer setup: %v", err)
		return
	}
	p, err := m.startPuzzle(s, i.GuildID, i.ChannelID, "🧩 The puzzle moved to <#"+i.ChannelID+">.")
	if err != nil {
		m.config.Logger.Errorf("slide puzzle: setup in %s: %v", i.ChannelID, err)
		m.editResponse(s, i, "❌ Could not start the puzzle: "+err.Error())
		return
	}
	m.editResponse(s, i, fmt.Sprintf("✅ Started a %dx%d puzzle here. Reset it or add images from /config → Slide Puzzle.", p.Grid, p.Grid))
}

// HandleComponent routes sp: buttons and selects.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.MessageComponentData().CustomID == helpButtonID {
		m.respondEphemeral(s, i, helpText)
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	cid := i.MessageComponentData().CustomID
	switch {
	case cid == voteButtonID:
		m.handleVoteButton(s, i)
	case strings.HasPrefix(cid, pickPrefix):
		m.handlePick(s, i, strings.TrimPrefix(cid, pickPrefix))
	case cid == resetButtonID:
		m.handleResetButton(s, i)
	case cid == resetConfirmID:
		m.handleResetConfirm(s, i)
	case cid == uploadButtonID:
		m.handleUploadButton(s, i)
	default:
		m.respondEphemeral(s, i, "❌ Unknown puzzle action.")
	}
}

// HandleModalSubmit routes sp: modal submissions.
func (m *Module) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	if i.ModalSubmitData().CustomID == uploadModalID {
		m.handleUploadSubmit(s, i)
		return
	}
	m.respondEphemeral(s, i, "❌ Unknown puzzle form.")
}

// handleVoteButton shows the voter an ephemeral menu of the tiles that can move.
func (m *Module) handleVoteButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	p, ok, err := m.store.GetSlidePuzzle(i.GuildID)
	if err != nil {
		m.config.Logger.Errorf("slide puzzle: load for vote: %v", err)
		m.respondEphemeral(s, i, "❌ Could not load the puzzle. Try again in a moment.")
		return
	}
	if !ok || i.Message == nil || p.MessageID != i.Message.ID {
		m.respondEphemeral(s, i, "This puzzle is no longer active.")
		return
	}
	if p.Solved {
		m.respondEphemeral(s, i, "🎉 This puzzle is already solved! A moderator can start a new one.")
		return
	}
	if !m.now().Before(p.RoundEnds) {
		m.respondEphemeral(s, i, roundClosedMsg)
		return
	}
	if tile, voted, err := m.store.SlidePuzzleUserVote(i.GuildID, p.Round, userID(i)); err != nil {
		m.config.Logger.Errorf("slide puzzle: read vote: %v", err)
		m.respondEphemeral(s, i, "❌ Could not check your vote. Try again in a moment.")
		return
	} else if voted {
		m.respondEphemeral(s, i, fmt.Sprintf("🗳️ You already voted for tile **%d** this round. The next move happens <t:%d:R>.", tile, p.RoundEnds.Unix()))
		return
	}

	var opts []discordgo.SelectMenuOption
	for _, t := range movableTiles(p.Board, p.Grid) {
		opts = append(opts, discordgo.SelectMenuOption{
			Label:       fmt.Sprintf("Tile %d", t),
			Value:       fmt.Sprint(t),
			Description: "Slides " + moveDirection(p.Board, p.Grid, t) + " into the gap",
		})
	}
	_ = m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Which tile should slide into the gap? You get one vote per round; the round ends <t:%d:R>.", p.RoundEnds.Unix()),
			Flags:   discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.SelectMenu{
					CustomID:    fmt.Sprintf("%s%d", pickPrefix, p.Round),
					Placeholder: "Pick a tile to move",
					Options:     opts,
				},
			}}},
		},
	})
}

// handlePick records a vote from the tile menu. The interaction is deferred
// before taking the guild lock (which may be held during slow Discord calls),
// and every result is delivered by editing the deferred response.
func (m *Module) handlePick(s *discordgo.Session, i *discordgo.InteractionCreate, roundStr string) {
	var round int64
	if _, err := fmt.Sscan(roundStr, &round); err != nil {
		m.updateEphemeral(s, i, "❌ That vote menu is broken. Click Vote again.")
		return
	}
	vals := i.MessageComponentData().Values
	var tile int
	if len(vals) != 1 {
		m.updateEphemeral(s, i, "❌ Pick one tile.")
		return
	}
	if _, err := fmt.Sscan(vals[0], &tile); err != nil {
		m.updateEphemeral(s, i, "❌ Pick one tile.")
		return
	}
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		m.config.Logger.Warnf("slide puzzle: defer vote: %v", err)
		return
	}

	lock := m.guildLock(i.GuildID)
	lock.Lock()
	result, ok := m.castVote(i, round, tile)
	lock.Unlock()
	m.editEphemeral(s, i, result)
	if ok {
		m.scheduleRedraw(i.GuildID)
	}
}

// redrawDelay batches the board redraws for a burst of votes into one edit.
const redrawDelay = 2 * time.Second

// scheduleRedraw publishes the board (with fresh vote counts) shortly after a
// vote. Votes arriving before it runs share the same redraw, so a busy round
// doesn't queue one image upload per voter. If it fails, the needs_redraw flag
// set with the vote makes the scheduler retry.
func (m *Module) scheduleRedraw(guildID string) {
	if m.afterFunc == nil {
		m.flushRedraw(guildID)
		return
	}
	m.timerMu.Lock()
	defer m.timerMu.Unlock()
	if m.redraws == nil {
		m.redraws = map[string]*time.Timer{}
	}
	if m.redraws[guildID] != nil {
		return
	}
	m.redraws[guildID] = m.afterFunc(redrawDelay, func() {
		m.timerMu.Lock()
		delete(m.redraws, guildID)
		m.timerMu.Unlock()
		m.flushRedraw(guildID)
	})
}

// flushRedraw publishes a pending board redraw from a fresh read under the
// guild lock, so an older snapshot never overwrites a newer move or reset.
func (m *Module) flushRedraw(guildID string) {
	lock := m.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()
	p, ok, err := m.store.GetSlidePuzzle(guildID)
	if err != nil || !ok || !p.NeedsRedraw || p.MessageID == "" {
		return
	}
	if err := m.publish(&p); err != nil {
		m.config.Logger.Warnf("slide puzzle: redraw board after vote: %v", err)
	}
}

// castVote validates and stores a vote. It returns the message for the voter
// and whether a new vote was recorded. Callers hold the guild lock.
func (m *Module) castVote(i *discordgo.InteractionCreate, round int64, tile int) (string, bool) {
	p, ok, err := m.store.GetSlidePuzzle(i.GuildID)
	if err != nil || !ok {
		return "❌ Could not load the puzzle. Try again in a moment.", false
	}
	if p.Round != round || p.Solved {
		return roundOverMsg, false
	}
	if _, movable := applyMove(p.Board, p.Grid, tile); !movable {
		return "❌ That tile can't move right now. Click Vote again.", false
	}
	got, inserted, err := m.store.CastSlidePuzzleVote(i.GuildID, round, userID(i), tile, m.now())
	switch {
	case errors.Is(err, database.ErrSlidePuzzleStale):
		return roundOverMsg, false
	case errors.Is(err, database.ErrSlidePuzzleRoundClosed):
		return roundClosedMsg, false
	case err != nil:
		m.config.Logger.Errorf("slide puzzle: cast vote: %v", err)
		return "❌ Could not save your vote. Try again in a moment.", false
	case !inserted:
		return fmt.Sprintf("🗳️ You already voted for tile **%d** this round.", got), false
	}
	return fmt.Sprintf("✅ Vote locked in for tile **%d** (slides %s). The top move happens <t:%d:R>.",
		tile, moveDirection(p.Board, p.Grid, tile), p.RoundEnds.Unix()), true
}

const (
	roundOverMsg   = "⌛ That round is over. Click Vote on the puzzle to vote in the current one."
	roundClosedMsg = "⌛ Voting for this round has closed. The move is being made now; vote again in the next round in a minute."
)

// editEphemeral replaces a deferred vote menu with a result message.
func (m *Module) editEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	empty := []discordgo.MessageComponent{}
	if err := m.ops.EditResponse(s, i.Interaction, &discordgo.WebhookEdit{Content: &content, Components: &empty}); err != nil {
		m.config.Logger.Warnf("slide puzzle: edit vote response: %v", err)
	}
}

// updateEphemeral replaces the ephemeral vote menu with a result message.
func (m *Module) updateEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    content,
			Components: []discordgo.MessageComponent{},
		},
	})
}

func (m *Module) handleResetButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondEphemeral(s, i, "❌ You need the Ban Members permission to reset the puzzle.")
		return
	}
	p, ok, err := m.store.GetSlidePuzzle(i.GuildID)
	if err != nil {
		m.respondEphemeral(s, i, "❌ Could not load the puzzle.")
		return
	}
	if !ok {
		m.respondEphemeral(s, i, "There's no puzzle yet. Run `/slidepuzzle-setup` in the channel you want it in.")
		return
	}
	grid := m.config.ForGuild(i.GuildID).GetSlidePuzzleGridSize()
	_ = m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Reset the puzzle in <#%s>? This picks a random image, shuffles a new %dx%d board and clears all votes and totals (%d votes, %d moves so far).",
				p.ChannelID, grid, grid, p.TotalVotes, p.TotalMoves),
			Flags: discordgo.MessageFlagsEphemeral,
			Components: []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Yes, reset it", Style: discordgo.DangerButton, CustomID: resetConfirmID},
			}}},
		},
	})
}

func (m *Module) handleResetConfirm(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondEphemeral(s, i, "❌ You need the Ban Members permission to reset the puzzle.")
		return
	}
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{Type: discordgo.InteractionResponseDeferredMessageUpdate}); err != nil {
		m.config.Logger.Warnf("slide puzzle: defer reset: %v", err)
		return
	}
	// An empty channel makes startPuzzle reuse the puzzle's current channel,
	// read under the guild lock, so a concurrent setup that moved it wins.
	np, err := m.startPuzzle(s, i.GuildID, "", "🔄 This puzzle was reset.")
	msg := fmt.Sprintf("✅ Reset! A new %dx%d puzzle is up in <#%s>.", np.Grid, np.Grid, np.ChannelID)
	if err != nil {
		m.config.Logger.Errorf("slide puzzle: reset: %v", err)
		msg = "❌ Could not reset the puzzle: " + err.Error()
	}
	empty := []discordgo.MessageComponent{}
	if err := m.ops.EditResponse(s, i.Interaction, &discordgo.WebhookEdit{Content: &msg, Components: &empty}); err != nil {
		m.config.Logger.Warnf("slide puzzle: edit reset response: %v", err)
	}
}

func (m *Module) handleUploadButton(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondEphemeral(s, i, "❌ You need the Ban Members permission to add puzzle images.")
		return
	}
	_ = m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseModal,
		Data: &discordgo.InteractionResponseData{
			CustomID: uploadModalID,
			Title:    "Add a puzzle image",
			Components: []discordgo.MessageComponent{discordgo.Label{
				Label:       "Image",
				Description: "PNG, JPEG, GIF or WebP up to 10 MB. Used on a future reset.",
				Component: discordgo.FileUpload{
					CustomID:  uploadFileID,
					MinValues: new(1),
					MaxValues: 1,
					Required:  new(true),
				},
			}},
		},
	})
}

func (m *Module) handleUploadSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.canManage(i) {
		m.respondEphemeral(s, i, "❌ You need the Ban Members permission to add puzzle images.")
		return
	}
	var att *discordgo.MessageAttachment
	for _, a := range i.ModalSubmitData().Resolved.Attachments {
		att = a
		break
	}
	if att == nil {
		m.respondEphemeral(s, i, "❌ No image was attached.")
		return
	}
	if !strings.HasPrefix(att.ContentType, "image/") {
		m.respondEphemeral(s, i, "❌ That file isn't an image. Use PNG, JPEG, GIF or WebP.")
		return
	}
	if att.Size > maxUploadBytes {
		m.respondEphemeral(s, i, "❌ That image is over 10 MB.")
		return
	}
	if err := m.deferEphemeral(s, i); err != nil {
		m.config.Logger.Warnf("slide puzzle: defer upload: %v", err)
		return
	}
	data, err := m.ops.Download(att.URL, maxUploadBytes)
	if err != nil {
		m.config.Logger.Warnf("slide puzzle: download upload: %v", err)
		m.editResponse(s, i, "❌ Could not download that image. Try again.")
		return
	}
	stored, err := normalizeUpload(data)
	if err != nil {
		m.editResponse(s, i, "❌ "+err.Error())
		return
	}
	if _, err := m.store.AddSlidePuzzleImage(i.GuildID, att.Filename, stored, userID(i), m.now()); err != nil {
		m.config.Logger.Errorf("slide puzzle: store upload: %v", err)
		m.editResponse(s, i, "❌ Could not save that image.")
		return
	}
	ids, _ := m.store.SlidePuzzleImageIDs(i.GuildID)
	m.editResponse(s, i, fmt.Sprintf("✅ Added **%s** to the puzzle pool (%d images including the default horse). It can come up on the next reset.",
		escapeName(att.Filename), len(ids)+1))
}

// pickImage returns a random image from the guild's pool: the built-in horse
// plus every uploaded image.
func (m *Module) pickImage(guildID string) (int64, error) {
	ids, err := m.store.SlidePuzzleImageIDs(guildID)
	if err != nil {
		return 0, err
	}
	n := m.randN(len(ids) + 1)
	if n == 0 {
		return 0, nil
	}
	return ids[n-1], nil
}

func (m *Module) loadImageBytes(guildID string, id int64) ([]byte, error) {
	if id == 0 {
		return defaultImage, nil
	}
	data, ok, err := m.store.GetSlidePuzzleImage(guildID, id)
	if err != nil {
		return nil, err
	}
	if !ok {
		return defaultImage, nil
	}
	return data, nil
}

// startPuzzle shuffles a new board, posts it in channelID and only then
// replaces the stored puzzle, so a failed post leaves the current puzzle
// untouched. The previous board message (if any) is retired with retiredNote.
// An empty channelID restarts the puzzle in its current channel.
func (m *Module) startPuzzle(s *discordgo.Session, guildID, channelID, retiredNote string) (database.SlidePuzzle, error) {
	lock := m.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()

	old, hadOld, err := m.store.GetSlidePuzzle(guildID)
	if err != nil {
		return database.SlidePuzzle{}, err
	}
	if channelID == "" {
		if !hadOld {
			return database.SlidePuzzle{}, errors.New("there's no puzzle to reset; run /slidepuzzle-setup first")
		}
		channelID = old.ChannelID
	}
	gc := m.config.ForGuild(guildID)
	grid := gc.GetSlidePuzzleGridSize()
	imageID, err := m.pickImage(guildID)
	if err != nil {
		return database.SlidePuzzle{}, err
	}
	now := m.now()
	p := database.SlidePuzzle{
		GuildID:   guildID,
		ChannelID: channelID,
		ImageID:   imageID,
		Grid:      grid,
		Board:     shuffledBoard(grid, m.randN),
		RoundEnds: now.Add(gc.GetSlidePuzzleRoundDuration()),
	}

	img, err := m.renderPuzzle(p)
	if err != nil {
		return database.SlidePuzzle{}, err
	}
	msg, err := m.ops.SendMessage(s, channelID, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{boardEmbed(p)},
		Components:      boardComponents(p),
		Files:           []*discordgo.File{{Name: boardFile, ContentType: "image/jpeg", Reader: bytes.NewReader(img)}},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		return database.SlidePuzzle{}, fmt.Errorf("could not post the board (check my permissions in that channel): %w", err)
	}
	p.ChannelID, p.MessageID = msg.ChannelID, msg.ID

	p, err = m.store.StartSlidePuzzle(p, now)
	if err != nil {
		if derr := m.ops.DeleteMessage(s, msg.ChannelID, msg.ID); derr != nil {
			m.config.Logger.Warnf("slide puzzle: delete unsaved board %s: %v", msg.ID, derr)
		}
		return database.SlidePuzzle{}, err
	}

	if hadOld && old.MessageID != "" && old.MessageID != p.MessageID {
		content := retiredNote
		empty := []discordgo.MessageComponent{}
		if err := m.ops.EditMessage(s, &discordgo.MessageEdit{
			Channel: old.ChannelID, ID: old.MessageID, Content: &content, Components: &empty,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}); err != nil {
			m.config.Logger.Warnf("slide puzzle: retire old board %s: %v", old.MessageID, err)
		}
	}
	m.armRoundTimer(p)
	return p, nil
}

// roundTimerSlack is added to each round timer so the deadline has definitely
// passed when it fires.
const roundTimerSlack = 500 * time.Millisecond

// armRoundTimer (re)schedules resolution of p's round at its exact end time.
func (m *Module) armRoundTimer(p database.SlidePuzzle) {
	if m.afterFunc == nil {
		return
	}
	m.timerMu.Lock()
	defer m.timerMu.Unlock()
	if m.timers == nil {
		m.timers = map[string]*time.Timer{}
	}
	if t := m.timers[p.GuildID]; t != nil {
		t.Stop()
		delete(m.timers, p.GuildID)
	}
	if p.Solved || p.MessageID == "" {
		return
	}
	d := p.RoundEnds.Sub(m.now())
	if d <= 0 {
		// Overdue (e.g. a resolution failed): leave the retry to the minute
		// tick rather than spinning on a tight timer.
		return
	}
	guildID := p.GuildID
	d += roundTimerSlack
	m.timers[guildID] = m.afterFunc(d, func() {
		if err := m.resolveRound(guildID); err != nil {
			m.config.Logger.Errorf("slide puzzle: resolve round for guild %s: %v", guildID, err)
		}
	})
}

func (m *Module) renderPuzzle(p database.SlidePuzzle) ([]byte, error) {
	data, err := m.loadImageBytes(p.GuildID, p.ImageID)
	if err != nil {
		return nil, err
	}
	src, err := decodeImage(data)
	if err != nil {
		return nil, err
	}
	var votes map[int]int
	if !p.Solved {
		tally, err := m.store.SlidePuzzleTally(p.GuildID, p.Round)
		if err != nil {
			return nil, err
		}
		votes = map[int]int{}
		for _, t := range movableTiles(p.Board, p.Grid) {
			votes[t] = tally[t]
		}
	}
	return renderBoard(src, p.Grid, p.Board, p.Solved, votes)
}

func boardEmbed(p database.SlidePuzzle) *discordgo.MessageEmbed {
	e := &discordgo.MessageEmbed{
		Title: "🧩 Community Slide Puzzle",
		Color: puzzleColor,
		Image: &discordgo.MessageEmbedImage{URL: "attachment://" + boardFile},
		Fields: []*discordgo.MessageEmbedField{
			{Name: "🗳️ Total votes cast", Value: fmt.Sprint(p.TotalVotes), Inline: true},
			{Name: "🔀 Moves made", Value: fmt.Sprint(p.TotalMoves), Inline: true},
		},
	}
	if p.Solved {
		e.Color = solvedColor
		e.Description = fmt.Sprintf("🎉 **Solved!** The picture is complete after %d moves and %d votes. Nice teamwork!", p.TotalMoves, p.TotalVotes)
		return e
	}
	e.Description = fmt.Sprintf("⏳ **Next move:** <t:%d:R>", p.RoundEnds.Unix())
	return e
}

const helpText = "Slide the tiles back into place, together! Hit **Vote** and pick a numbered tile next to the gap. " +
	"When the round ends, the most-voted tile slides into the gap (ties are a coin flip). One vote per person per round."

func boardComponents(p database.SlidePuzzle) []discordgo.MessageComponent {
	if p.Solved {
		return []discordgo.MessageComponent{}
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{
		discordgo.Button{Label: "Vote", Emoji: &discordgo.ComponentEmoji{Name: "🗳️"}, Style: discordgo.PrimaryButton, CustomID: voteButtonID},
		discordgo.Button{Label: "Help", Emoji: &discordgo.ComponentEmoji{Name: "❓"}, Style: discordgo.SecondaryButton, CustomID: helpButtonID},
	}}}
}

// refreshBoardText updates the board's embed (counters and timer) without
// re-uploading the image.
func (m *Module) refreshBoardText(s *discordgo.Session, p database.SlidePuzzle) error {
	embeds := []*discordgo.MessageEmbed{boardEmbed(p)}
	return m.ops.EditMessage(s, &discordgo.MessageEdit{
		Channel: p.ChannelID, ID: p.MessageID, Embeds: &embeds,
	})
}

// publishText refreshes the board text and clears the pending-refresh flag.
// On failure the flag stays set and the next tick retries.
func (m *Module) publishText(p *database.SlidePuzzle) error {
	if err := m.refreshBoardText(m.session, *p); err != nil {
		if recovered, rerr := m.recoverBoard(p, err); recovered {
			return rerr
		}
		return fmt.Errorf("refresh board text (will retry): %w", err)
	}
	return m.store.MarkSlidePuzzleRefreshed(p.GuildID, p.Round)
}

// discordCode returns the Discord JSON error code of a REST error, or 0.
func discordCode(err error) int {
	var restErr *discordgo.RESTError
	if errors.As(err, &restErr) && restErr.Message != nil {
		return restErr.Message.Code
	}
	return 0
}

// recoverBoard handles a board message that no longer exists. If the message
// was deleted, a fresh board is posted in the same channel so the puzzle keeps
// going. If the channel is gone, the puzzle is paused (no board message) until
// a moderator runs setup somewhere else. It reports whether err was one of
// those cases; p is updated to the new message on success.
func (m *Module) recoverBoard(p *database.SlidePuzzle, err error) (bool, error) {
	switch discordCode(err) {
	case discordgo.ErrCodeUnknownMessage:
	case discordgo.ErrCodeUnknownChannel:
		return true, m.pauseBoard(p)
	default:
		return false, nil
	}
	img, err := m.renderPuzzle(*p)
	if err != nil {
		return true, err
	}
	msg, err := m.ops.SendMessage(m.session, p.ChannelID, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{boardEmbed(*p)},
		Components:      boardComponents(*p),
		Files:           []*discordgo.File{{Name: boardFile, ContentType: "image/jpeg", Reader: bytes.NewReader(img)}},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		if discordCode(err) == discordgo.ErrCodeUnknownChannel {
			return true, m.pauseBoard(p)
		}
		return true, fmt.Errorf("repost deleted board (will retry): %w", err)
	}
	if err := m.store.SetSlidePuzzleMessage(p.GuildID, msg.ChannelID, msg.ID); err != nil {
		if derr := m.ops.DeleteMessage(m.session, msg.ChannelID, msg.ID); derr != nil {
			m.config.Logger.Warnf("slide puzzle: delete unsaved board %s: %v", msg.ID, derr)
		}
		return true, err
	}
	m.config.Logger.Infof("slide puzzle: board in guild %s was deleted, reposted as %s", p.GuildID, msg.ID)
	p.ChannelID, p.MessageID = msg.ChannelID, msg.ID
	return true, m.store.MarkSlidePuzzlePublished(p.GuildID, p.Round)
}

func (m *Module) pauseBoard(p *database.SlidePuzzle) error {
	m.config.Logger.Warnf("slide puzzle: channel %s for guild %s is gone; pausing until setup is run again", p.ChannelID, p.GuildID)
	if err := m.store.SetSlidePuzzleMessage(p.GuildID, p.ChannelID, ""); err != nil {
		return err
	}
	p.MessageID = ""
	return nil
}

// redrawBoard re-renders the board image after a move.
func (m *Module) redrawBoard(s *discordgo.Session, p database.SlidePuzzle) error {
	img, err := m.renderPuzzle(p)
	if err != nil {
		return err
	}
	embeds := []*discordgo.MessageEmbed{boardEmbed(p)}
	components := boardComponents(p)
	return m.ops.EditMessage(s, &discordgo.MessageEdit{
		Channel:     p.ChannelID,
		ID:          p.MessageID,
		Embeds:      &embeds,
		Components:  &components,
		Files:       []*discordgo.File{{Name: boardFile, ContentType: "image/jpeg", Reader: bytes.NewReader(img)}},
		Attachments: &[]*discordgo.MessageAttachment{},
	})
}

// roundTick makes the top-voted move for every puzzle whose round has ended.
func (m *Module) roundTick() error {
	if m.store == nil || m.session == nil {
		return nil
	}
	due, err := m.store.DueSlidePuzzles(m.now())
	if err != nil {
		return err
	}
	var errs []error
	for _, p := range due {
		if err := m.resolveRound(p.GuildID); err != nil {
			errs = append(errs, fmt.Errorf("guild %s: %w", p.GuildID, err))
		}
	}
	return errors.Join(errs...)
}

// resolveRound publishes any pending board redraw, then ends the current
// round if it is due: the top-voted move is applied, or the round is extended
// when nobody voted.
func (m *Module) resolveRound(guildID string) error {
	lock := m.guildLock(guildID)
	lock.Lock()
	defer lock.Unlock()

	p, ok, err := m.store.GetSlidePuzzle(guildID)
	if err != nil || !ok || p.MessageID == "" {
		return err
	}
	// Whatever happens below, schedule the next resolution from p's final
	// state (a new round, an extended one, or a pause).
	defer func() { m.armRoundTimer(p) }()
	switch {
	case p.NeedsRedraw:
		if err := m.publish(&p); err != nil {
			return err
		}
	case p.NeedsRefresh:
		if err := m.publishText(&p); err != nil {
			return err
		}
	}
	now := m.now()
	if p.MessageID == "" || p.Solved || now.Before(p.RoundEnds) {
		return nil
	}
	roundLen := m.config.ForGuild(guildID).GetSlidePuzzleRoundDuration()
	tally, err := m.store.SlidePuzzleTally(guildID, p.Round)
	if err != nil {
		return err
	}
	tile, ok := pickWinner(tally, movableTiles(p.Board, p.Grid), m.randN)
	if !ok {
		p.RoundEnds = now.Add(roundLen)
		if err := m.store.ExtendSlidePuzzleRound(guildID, p.Round, p.RoundEnds); err != nil {
			return err
		}
		return m.publishText(&p)
	}

	board, _ := applyMove(p.Board, p.Grid, tile)
	solved := isSolved(board)
	if err := m.store.AdvanceSlidePuzzle(guildID, p.Round, board, solved, now.Add(roundLen), now); err != nil {
		if errors.Is(err, database.ErrSlidePuzzleStale) {
			return nil
		}
		return err
	}
	p.Board, p.Solved, p.RoundEnds = board, solved, now.Add(roundLen)
	p.Round++
	p.TotalMoves++
	return m.publish(&p)
}

// publish redraws the board image and clears the pending-redraw flag. On
// failure the flag stays set and the next tick retries.
func (m *Module) publish(p *database.SlidePuzzle) error {
	if err := m.redrawBoard(m.session, *p); err != nil {
		if recovered, rerr := m.recoverBoard(p, err); recovered {
			return rerr
		}
		return fmt.Errorf("redraw board (will retry): %w", err)
	}
	return m.store.MarkSlidePuzzlePublished(p.GuildID, p.Round)
}

// Service runs the round scheduler.
type Service struct {
	m *Module
}

// HydrateServiceDiscordSession stores the session and resolves any rounds
// that ended while the bot was down.
func (s *Service) HydrateServiceDiscordSession(session *discordgo.Session) error {
	s.m.session = session
	go func() {
		if err := s.m.roundTick(); err != nil {
			s.m.config.Logger.Errorf("slide puzzle: startup round check failed: %v", err)
		}
		if err := s.m.armAllRoundTimers(); err != nil {
			s.m.config.Logger.Errorf("slide puzzle: arm round timers: %v", err)
		}
	}()
	return nil
}

// armAllRoundTimers schedules every active puzzle's round end after startup.
func (m *Module) armAllRoundTimers() error {
	if m.store == nil {
		return nil
	}
	// Every active puzzle's deadline is before the far future, so this lists
	// them all.
	active, err := m.store.DueSlidePuzzles(m.now().AddDate(100, 0, 0))
	if err != nil {
		return err
	}
	for _, p := range active {
		m.armRoundTimer(p)
	}
	return nil
}

// ScheduledFuncs checks for finished rounds every minute, as a fallback for
// the exact per-round timers.
func (s *Service) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@every 1m": s.m.roundTick,
	}
}

// escapeName strips Discord markdown and mention characters from a filename.
func escapeName(name string) string {
	r := strings.NewReplacer("*", "", "_", "\\_", "`", "", "~", "", "|", "", "@", "", "<", "", ">", "")
	runes := []rune(r.Replace(name))
	if len(runes) > 80 {
		runes = runes[:80]
	}
	return string(runes)
}
