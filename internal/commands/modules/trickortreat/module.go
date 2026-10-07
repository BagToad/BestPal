// Package trickortreat runs the Halloween candy bowl event: bowls spawn in
// configured channels, members grab treats until a bowl is empty, then get a
// 30 minute window to click TRICK! for a random outcome.
package trickortreat

import (
	"embed"
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

//go:embed assets/bowl_*.png
var assets embed.FS

const (
	// trickWindow is how long TRICK! stays up once a bowl is emptied.
	trickWindow = 30 * time.Minute
	// leaderboardSize is how many users /candy-leaderboard lists.
	leaderboardSize = 10
	// maxDescription stays under Discord's 4096 character embed description limit.
	maxDescription = 4000

	treatButtonID = "tot:treat"
	trickButtonID = "tot:trick"

	bowlColor = 0xFF7518
)

// store is the persistence the module needs; *database.DB satisfies it.
type store interface {
	CreateBowl(guildID, channelID, messageID string, now time.Time) error
	GetBowl(messageID string) (database.Bowl, []database.BowlLogEntry, bool, error)
	ActiveBowlChannels(guildID string) (map[string]bool, error)
	ExpiredBowls(now time.Time) ([]database.Bowl, error)
	ArchiveBowl(messageID string) error
	ResetBowl(messageID string) (database.Bowl, bool, error)
	DrainBowl(messageID string, now time.Time, trickWindow time.Duration, leave int, logLine func(remaining int) string) (database.Bowl, bool, error)
	ClaimTreat(messageID, userID string, now time.Time, trickWindow time.Duration, logLine func(database.TreatResult) string) (database.TreatResult, error)
	ApplyTrick(messageID, userID string, now time.Time, apply func(database.TrickTx) (string, error)) (database.TrickResult, error)
	GetBucket(guildID, userID string) (database.TOTUser, []database.Souvenir, int, error)
	CandyLeaderboard(guildID string, limit int) ([]database.CandyLeaderboardEntry, error)
}

// discordOps wraps the Discord calls the module makes so tests can capture them.
type discordOps struct {
	Respond       func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error
	SendMessage   func(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) (*discordgo.Message, error)
	EditMessage   func(s *discordgo.Session, edit *discordgo.MessageEdit) error
	DeleteMessage func(s *discordgo.Session, channelID, messageID string) error
}

func defaultDiscordOps() discordOps {
	return discordOps{
		Respond: func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
			return s.InteractionRespond(i, resp)
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
	}
}

// Module implements the trick-or-treat event.
type Module struct {
	config  *config.Config
	store   store
	ops     discordOps
	session *discordgo.Session
	service *Service
	now     func() time.Time
	// randN returns a uniform random int in [0, n); randFloat one in [0, 1).
	randN     func(n int) int
	randFloat func() float64

	mu    sync.Mutex
	locks map[string]*sync.Mutex
	// nextSpawnRoll is when the spawner next rolls for bowls.
	nextSpawnRoll time.Time
}

// New creates the trick-or-treat module.
func New(deps *types.Dependencies) *Module {
	m := &Module{
		config:    deps.Config,
		ops:       defaultDiscordOps(),
		session:   deps.Session,
		now:       time.Now,
		randN:     rand.IntN,
		randFloat: rand.Float64,
		locks:     map[string]*sync.Mutex{},
	}
	if deps.DB != nil {
		m.store = deps.DB
	}
	m.service = &Service{m: m}
	return m
}

// Service returns the module's scheduler-backed service.
func (m *Module) Service() types.ModuleService { return m.service }

// ConfigSettings declares the event's per-guild settings.
func (m *Module) ConfigSettings() []config.Setting {
	return []config.Setting{
		{
			Key:         config.KeyTrickOrTreatEnabled,
			Category:    config.CategoryTrickOrTreat,
			Label:       "Candy bowls spawn",
			Description: "Master switch for candy bowls spawning on their own. /spawn-bowl works either way.",
			Kind:        config.KindBool,
			Default:     false,
		},
		{
			Key:         config.KeyTrickOrTreatChannels,
			Category:    config.CategoryTrickOrTreat,
			Label:       "Candy bowl channels",
			Description: "Channels candy bowls can spawn in. Each gets a 10–15% chance per spawn roll.",
			Kind:        config.KindChannelList,
		},
		{
			Key:         config.KeyTrickOrTreatSpawnInterval,
			Category:    config.CategoryTrickOrTreat,
			Label:       "Spawn roll interval",
			Description: "Average time between spawn rolls, randomized ±50% (e.g. 1h).",
			Kind:        config.KindDuration,
			Default:     "1h",
		},
	}
}

func (m *Module) bowlLock(messageID string) *sync.Mutex {
	m.mu.Lock()
	defer m.mu.Unlock()
	l, ok := m.locks[messageID]
	if !ok {
		l = &sync.Mutex{}
		m.locks[messageID] = l
	}
	return l
}

func (m *Module) forgetLock(messageID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.locks, messageID)
}

// Register adds /bucket, /candy-leaderboard, /spawn-bowl, /reset-bowl and
// /debug-empty-bowl.
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	guildOnly := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	var adminPerms int64 = discordgo.PermissionManageGuild
	var administrator int64 = discordgo.PermissionAdministrator

	cmds["bucket"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "bucket",
			Description: "Check a trick-or-treat bucket",
			Contexts:    guildOnly,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: "Whose bucket to show (defaults to you)",
			}},
		},
		HandlerFunc: m.handleBucket,
	}
	cmds["candy-leaderboard"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "candy-leaderboard",
			Description: "See who has the most candy",
			Contexts:    guildOnly,
		},
		HandlerFunc: m.handleLeaderboard,
	}
	cmds["spawn-bowl"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "spawn-bowl",
			Description:              "Put out a candy bowl now",
			Contexts:                 guildOnly,
			DefaultMemberPermissions: &adminPerms,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:         discordgo.ApplicationCommandOptionChannel,
				Name:         "channel",
				Description:  "Where to put it (defaults to this channel)",
				ChannelTypes: []discordgo.ChannelType{discordgo.ChannelTypeGuildText},
			}},
		},
		HandlerFunc: m.handleSpawn,
	}
	cmds["reset-bowl"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "reset-bowl",
			Description:              "Refill a candy bowl and reopen it",
			Contexts:                 guildOnly,
			DefaultMemberPermissions: &adminPerms,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message_id",
				Description: "The bowl's message ID or link",
				Required:    true,
			}},
		},
		HandlerFunc: m.handleReset,
	}
	cmds["debug-empty-bowl"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "debug-empty-bowl",
			Description:              "Debug: empty an active candy bowl so TRICK! shows now",
			Contexts:                 guildOnly,
			DefaultMemberPermissions: &administrator,
			Options: []*discordgo.ApplicationCommandOption{{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "message_id",
				Description: "The active bowl's message ID or link",
				Required:    true,
			}, {
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "mode",
				Description: "Empty it now (default), or leave the last treat for a real click",
				Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "Empty now (TRICK! is live)", Value: debugModeEmpty},
					{Name: "Leave last treat (grab it yourself)", Value: debugModeLastTreat},
				},
			}},
		},
		HandlerFunc: m.handleDebugEmpty,
	}
}

func (m *Module) respond(s *discordgo.Session, i *discordgo.InteractionCreate, data *discordgo.InteractionResponseData) {
	if data.AllowedMentions == nil {
		data.AllowedMentions = &discordgo.MessageAllowedMentions{}
	}
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: data,
	}); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to respond: %v", err)
	}
}

func (m *Module) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	m.respond(s, i, &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral})
}

func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

// Bowl rendering
// -----

func bowlImageName(remaining int) string { return fmt.Sprintf("bowl_%d.png", remaining) }

func bowlImage(remaining int) (*discordgo.File, error) {
	name := bowlImageName(remaining)
	f, err := assets.Open("assets/" + name)
	if err != nil {
		return nil, err
	}
	return &discordgo.File{Name: name, ContentType: "image/png", Reader: f}, nil
}

func bowlEmbed(b database.Bowl, log []database.BowlLogEntry) *discordgo.MessageEmbed {
	var desc string
	switch {
	case !b.Active:
		desc = "This bowl is all done. Keep an eye out for the next one! 🎃"
	case b.Remaining > 0:
		desc = "Grab a treat before the bowl is empty!"
	default:
		desc = fmt.Sprintf("The bowl is empty! Click **TRICK!** for some mischief. Closes <t:%d:R>.", b.ExpiresAt.Unix())
	}

	lines := make([]string, len(log))
	for k, e := range log {
		lines[k] = fmt.Sprintf("• <t:%d:T> %s", e.At.Unix(), e.Text)
	}
	header := "\n\n**Action Log:**\n"
	body := strings.Join(lines, "\n")
	if len(log) == 0 {
		body = "Nobody has grabbed a treat yet."
	}
	for len(lines) > 1 && len(desc)+len(header)+len(body) > maxDescription {
		lines = lines[1:]
		body = "…\n" + strings.Join(lines, "\n")
	}

	return &discordgo.MessageEmbed{
		Title:       "🎃 A Wild Candy Bowl Appeared!",
		Description: desc + header + body,
		Color:       bowlColor,
		Image:       &discordgo.MessageEmbedImage{URL: "attachment://" + bowlImageName(b.Remaining)},
	}
}

func bowlComponents(b database.Bowl) []discordgo.MessageComponent {
	if !b.Active {
		return []discordgo.MessageComponent{}
	}
	button := discordgo.Button{Label: "Grab a Treat!", Style: discordgo.SuccessButton, CustomID: treatButtonID, Emoji: &discordgo.ComponentEmoji{Name: "🍬"}}
	if b.Remaining <= 0 {
		button = discordgo.Button{Label: "TRICK!", Style: discordgo.DangerButton, CustomID: trickButtonID, Emoji: &discordgo.ComponentEmoji{Name: "🎃"}}
	}
	return []discordgo.MessageComponent{discordgo.ActionsRow{Components: []discordgo.MessageComponent{button}}}
}

// renderBowl edits a bowl message to match its stored state. withImage
// re-uploads the bowl image for the current candy count.
func (m *Module) renderBowl(s *discordgo.Session, messageID string, withImage bool) error {
	b, log, ok, err := m.store.GetBowl(messageID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("bowl %s not found", messageID)
	}
	embeds := []*discordgo.MessageEmbed{bowlEmbed(b, log)}
	components := bowlComponents(b)
	edit := &discordgo.MessageEdit{
		ID:              b.MessageID,
		Channel:         b.ChannelID,
		Embeds:          &embeds,
		Components:      &components,
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
	if withImage {
		img, err := bowlImage(b.Remaining)
		if err != nil {
			return err
		}
		edit.Files = []*discordgo.File{img}
		edit.Attachments = &[]*discordgo.MessageAttachment{}
	}
	return m.ops.EditMessage(s, edit)
}

// spawnBowl posts a full bowl in a channel and records it.
func (m *Module) spawnBowl(s *discordgo.Session, guildID, channelID string) (string, error) {
	b := database.Bowl{Remaining: database.BowlSize, Active: true}
	img, err := bowlImage(b.Remaining)
	if err != nil {
		return "", err
	}
	msg, err := m.ops.SendMessage(s, channelID, &discordgo.MessageSend{
		Embeds:          []*discordgo.MessageEmbed{bowlEmbed(b, nil)},
		Components:      bowlComponents(b),
		Files:           []*discordgo.File{img},
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	})
	if err != nil {
		return "", fmt.Errorf("failed to post bowl: %w", err)
	}
	if err := m.store.CreateBowl(guildID, channelID, msg.ID, m.now()); err != nil {
		if delErr := m.ops.DeleteMessage(s, channelID, msg.ID); delErr != nil {
			m.config.Logger.Warnf("trick-or-treat: failed to delete unrecorded bowl %s: %v", msg.ID, delErr)
		}
		return "", err
	}
	return msg.ID, nil
}

// archiveBowl closes a bowl: buttons removed, log frozen.
func (m *Module) archiveBowl(s *discordgo.Session, messageID string) error {
	if err := m.store.ArchiveBowl(messageID); err != nil {
		return err
	}
	defer m.forgetLock(messageID)
	return m.renderBowl(s, messageID, false)
}

// removeButtons redraws a closed bowl without buttons as the click's response.
func (m *Module) removeButtons(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := &discordgo.InteractionResponseData{Components: []discordgo.MessageComponent{}}
	if b, log, ok, err := m.store.GetBowl(i.Message.ID); err == nil && ok {
		b.Active = false
		data.Embeds = []*discordgo.MessageEmbed{bowlEmbed(b, log)}
	} else if len(i.Message.Embeds) > 0 {
		data.Embeds = i.Message.Embeds
	}
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: data,
	}); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to clear stale buttons: %v", err)
	}
}

// Buttons
// -----

// HandleComponent routes candy bowl button clicks.
func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	if i.Message == nil {
		return
	}
	switch i.MessageComponentData().CustomID {
	case treatButtonID:
		m.handleTreat(s, i)
	case trickButtonID:
		m.handleTrick(s, i)
	}
}

// treatLogLine is the action log entry for a grab that left remaining
// candies in the bowl.
func treatLogLine(userID string, remaining, bonus int) string {
	line := mention(userID) + " grabbed a treat!"
	if remaining == 0 {
		line = mention(userID) + " grabbed the last treat!"
	}
	if bonus > 0 {
		line += fmt.Sprintf(" (+%d bonus)", bonus)
	}
	return line + fmt.Sprintf(" (%d left)", remaining)
}

func (m *Module) handleTreat(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := interactionUserID(i)
	messageID := i.Message.ID
	lock := m.bowlLock(messageID)
	lock.Lock()
	defer lock.Unlock()

	res, err := m.store.ClaimTreat(messageID, userID, m.now(), trickWindow, func(r database.TreatResult) string {
		return treatLogLine(userID, r.Bowl.Remaining, r.Bonus)
	})
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: treat failed: %v", err)
		m.respondEphemeral(s, i, "❌ Something went wrong grabbing that treat. Try again!")
		return
	}

	switch res.Status {
	case database.TreatBowlClosed:
		m.removeButtons(s, i)
		return
	case database.TreatAlreadyClaimed:
		m.respondEphemeral(s, i, "You have already claimed a treat from this bowl!")
		return
	case database.TreatBowlEmpty:
		m.respondEphemeral(s, i, "The bowl is empty! Try **TRICK!** instead.")
		return
	case database.TreatTimedOut:
		m.respondEphemeral(s, i, fmt.Sprintf("You're currently recovering from a trick! Remaining timeouts: %d", res.TimeoutCharges))
		return
	case database.TreatStillTimedOut:
		m.respondEphemeral(s, i, fmt.Sprintf("You're still recovering from a trick. Try the next bowl! Remaining timeouts: %d", res.TimeoutCharges))
		return
	}

	msg := fmt.Sprintf("🍬 You grabbed a treat! You now have **%s**.", candies(res.Candies))
	if res.Bonus > 0 {
		msg = fmt.Sprintf("🍬 You grabbed a treat plus **%d bonus** from your %s buff! You now have **%s**.", res.Bonus, res.BonusSource, candies(res.Candies))
	}
	m.respondEphemeral(s, i, msg)
	if err := m.renderBowl(s, messageID, true); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to update bowl %s: %v", messageID, err)
	}
}

func (m *Module) handleTrick(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := interactionUserID(i)
	messageID := i.Message.ID
	lock := m.bowlLock(messageID)
	lock.Lock()
	defer lock.Unlock()

	var result trickResult
	res, err := m.store.ApplyTrick(messageID, userID, m.now(), func(tx database.TrickTx) (string, error) {
		outcome := trickOutcomes[m.randN(len(trickOutcomes))]
		var err error
		result, err = outcome.Run(tx, userID, m.randN)
		if err != nil {
			return "", err
		}
		return mention(userID) + " triggered a TRICK: " + result.Log, nil
	})
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: trick failed: %v", err)
		m.respondEphemeral(s, i, "❌ Something went wrong with that trick. Try again!")
		return
	}

	switch res.Status {
	case database.TrickBowlClosed:
		m.removeButtons(s, i)
		return
	case database.TrickAlreadyDone:
		m.respondEphemeral(s, i, "You have already triggered a trick on this bowl!")
		return
	case database.TrickBowlNotEmpty:
		m.respondEphemeral(s, i, "There's still candy in this bowl!")
		return
	}

	outcome := "No change"
	if len(result.Effects) > 0 {
		outcome = strings.Join(result.Effects, " • ")
	}
	users := []string{userID}
	if result.Target != "" && result.Target != userID {
		users = append(users, result.Target)
	}
	m.respond(s, i, &discordgo.InteractionResponseData{
		Content:         fmt.Sprintf("🎃 **TRICK!** — %s\n%s\n\n⚡ **Outcome:** %s", mention(userID), result.Flavor, outcome),
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: users},
	})
	if err := m.renderBowl(s, messageID, false); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to update bowl %s: %v", messageID, err)
	}
}

// Commands
// -----

func (m *Module) handleBucket(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	target := interactionUserID(i)
	name := ""
	if i.Member != nil {
		name = memberName(i.Member)
	}
	data := i.ApplicationCommandData()
	for _, opt := range data.Options {
		if opt.Name == "user" {
			target = fmt.Sprint(opt.Value)
			name = ""
			if data.Resolved != nil {
				if mem, ok := data.Resolved.Members[target]; ok {
					if u, ok := data.Resolved.Users[target]; ok {
						mem.User = u
					}
					name = memberName(mem)
				} else if u, ok := data.Resolved.Users[target]; ok {
					name = u.DisplayName()
				}
			}
		}
	}
	if name == "" {
		name = "Someone"
	}

	u, items, rank, err := m.store.GetBucket(i.GuildID, target)
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to load bucket: %v", err)
		m.respondEphemeral(s, i, "❌ Failed to load that bucket.")
		return
	}

	stash := fmt.Sprintf("%d Candies  •  Rank #%d on /candy-leaderboard", u.Candies, rank)
	if u.Candies == 1 {
		stash = fmt.Sprintf("1 Candy  •  Rank #%d on /candy-leaderboard", rank)
	}
	var pocket []string
	for _, it := range items {
		info, ok := souvenirs[it.Type]
		if !ok {
			info = souvenirInfo{"❔", it.Type}
		}
		pocket = append(pocket, fmt.Sprintf("• %s %dx %s", info.emoji, it.Quantity, info.name))
	}
	if len(pocket) == 0 {
		pocket = []string{"Empty pockets."}
	}
	var status []string
	if u.BonusCandy > 0 {
		word := "Candies"
		if u.BonusCandy == 1 {
			word = "Candy"
		}
		status = append(status, fmt.Sprintf("• 🟢 +%d %s on Next Bucket Claim (%s Buff)", u.BonusCandy, word, u.BonusSource))
	}
	if u.TimeoutCharges > 0 {
		spawnWord := "Spawns"
		if u.TimeoutCharges == 1 {
			spawnWord = "Spawn"
		}
		status = append(status, fmt.Sprintf("• 🔴 Timed Out for Next %d Candy Bowl %s (%s Debuff)", u.TimeoutCharges, spawnWord, u.TimeoutSource))
	}
	if len(status) == 0 {
		status = []string{"None."}
	}

	m.respond(s, i, &discordgo.InteractionResponseData{Embeds: []*discordgo.MessageEmbed{{
		Title: fmt.Sprintf("🎃 %s's Trick-or-Treat Bucket", name),
		Color: bowlColor,
		Fields: []*discordgo.MessageEmbedField{
			{Name: "🍬 Candy Stash", Value: stash},
			{Name: "🎒 Pocket Souvenirs & Pocket Trash", Value: strings.Join(pocket, "\n")},
			{Name: "⚡ Active Status (Buffs & Debuffs)", Value: strings.Join(status, "\n")},
		},
	}}})
}

func memberName(mem *discordgo.Member) string {
	if mem.Nick != "" {
		return mem.Nick
	}
	if mem.User != nil {
		return mem.User.DisplayName()
	}
	return ""
}

func (m *Module) handleLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	entries, err := m.store.CandyLeaderboard(i.GuildID, leaderboardSize)
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to load leaderboard: %v", err)
		m.respondEphemeral(s, i, "❌ Failed to load the candy leaderboard.")
		return
	}
	if len(entries) == 0 {
		m.respond(s, i, &discordgo.InteractionResponseData{Content: "🍬 Nobody has any candy yet. Watch for a candy bowl!"})
		return
	}
	lines := []string{"🏆 **Candy Leaderboard**"}
	for _, e := range entries {
		lines = append(lines, fmt.Sprintf("%d. %s — %s", e.Rank, mention(e.UserID), candies(e.Candies)))
	}
	m.respond(s, i, &discordgo.InteractionResponseData{Content: strings.Join(lines, "\n")})
}

func (m *Module) handleSpawn(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	channelID := i.ChannelID
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "channel" {
			channelID = fmt.Sprint(opt.Value)
		}
	}
	id, err := m.spawnBowl(s, i.GuildID, channelID)
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: manual spawn failed: %v", err)
		m.respondEphemeral(s, i, "❌ Couldn't put out a bowl there. Can I post in that channel?")
		return
	}
	m.respondEphemeral(s, i, fmt.Sprintf("🎃 Candy bowl is out in <#%s> (message `%s`).", channelID, id))
}

// parseMessageID accepts a raw message ID or a message link.
func parseMessageID(v string) string {
	v = strings.TrimSpace(v)
	if k := strings.LastIndex(v, "/"); k >= 0 {
		v = v[k+1:]
	}
	for _, r := range v {
		if r < '0' || r > '9' {
			return ""
		}
	}
	return v
}

func (m *Module) handleReset(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	var raw string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "message_id" {
			raw = fmt.Sprint(opt.Value)
		}
	}
	messageID := parseMessageID(raw)
	if messageID == "" {
		m.respondEphemeral(s, i, "❌ That doesn't look like a message ID or link.")
		return
	}

	lock := m.bowlLock(messageID)
	lock.Lock()
	defer lock.Unlock()

	b, ok, err := m.store.ResetBowl(messageID)
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: reset failed: %v", err)
		m.respondEphemeral(s, i, "❌ Failed to reset that bowl.")
		return
	}
	if !ok || b.GuildID != i.GuildID {
		m.respondEphemeral(s, i, "❌ I don't know a candy bowl with that message ID.")
		return
	}
	if err := m.renderBowl(s, messageID, true); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to redraw reset bowl %s: %v", messageID, err)
		m.respondEphemeral(s, i, "⚠️ The bowl is reset, but I couldn't update its message.")
		return
	}
	m.respondEphemeral(s, i, fmt.Sprintf("🎃 Bowl refilled with %d candies and reopened.", database.BowlSize))
}

const (
	debugModeEmpty     = "empty"
	debugModeLastTreat = "last-treat"
)

// handleDebugEmpty drains an active bowl the way real grabs would, logging a
// grab by the admin for each candy removed. The default mode empties it
// (bowl_0.png, TRICK! button, fresh 30-minute trick window); last-treat leaves
// one candy so a real "Grab a Treat!" click empties it organically.
func (m *Module) handleDebugEmpty(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Checked here as well as in DefaultMemberPermissions, which server
	// owners can override per role.
	if i.Member == nil || i.Member.Permissions&discordgo.PermissionAdministrator == 0 {
		m.respondEphemeral(s, i, "❌ You must be an Administrator to use debug commands.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	var raw string
	mode := debugModeEmpty
	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "message_id":
			raw = fmt.Sprint(opt.Value)
		case "mode":
			mode = fmt.Sprint(opt.Value)
		}
	}
	leave := 0
	if mode == debugModeLastTreat {
		leave = 1
	}
	messageID := parseMessageID(raw)
	if messageID == "" {
		m.respondEphemeral(s, i, "❌ That doesn't look like a message ID or link.")
		return
	}

	lock := m.bowlLock(messageID)
	lock.Lock()
	defer lock.Unlock()

	if b, _, ok, err := m.store.GetBowl(messageID); err == nil && ok && b.GuildID != i.GuildID {
		m.respondEphemeral(s, i, "❌ Couldn't find an active candy bowl with that message ID.")
		return
	}
	adminID := interactionUserID(i)
	b, ok, err := m.store.DrainBowl(messageID, m.now(), trickWindow, leave, func(remaining int) string {
		return treatLogLine(adminID, remaining, 0)
	})
	if err != nil {
		m.config.Logger.Errorf("trick-or-treat: debug empty failed: %v", err)
		m.respondEphemeral(s, i, "❌ Failed to empty that bowl.")
		return
	}
	if !ok {
		m.respondEphemeral(s, i, "❌ Couldn't find an active candy bowl with that message ID.")
		return
	}
	if b.Remaining < leave {
		m.respondEphemeral(s, i, "❌ That bowl is already empty. Use `/reset-bowl` to refill it first.")
		return
	}
	if err := m.renderBowl(s, messageID, true); err != nil {
		m.config.Logger.Errorf("trick-or-treat: failed to redraw emptied bowl %s: %v", messageID, err)
		m.respondEphemeral(s, i, "⚠️ The bowl is empty, but I couldn't update its message.")
		return
	}
	if leave > 0 {
		m.respondEphemeral(s, i, "🍬 One treat left. Click **Grab a Treat!** (from an account that hasn't grabbed from this bowl yet) to empty it like a real player would.")
		return
	}
	m.respondEphemeral(s, i, "🎃 Bowl emptied. TRICK! is live for the next 30 minutes.")
}

// Scheduled work
// -----

// spawnTick rolls for new bowls when a spawn roll is due.
func (m *Module) spawnTick() error {
	if m.store == nil || m.session == nil {
		return nil
	}
	gc := m.config.PrimaryGuild()
	interval := gc.GetTrickOrTreatSpawnInterval()
	now := m.now()
	if m.nextSpawnRoll.IsZero() {
		// Start partway into an interval so restarts don't delay spawns.
		m.nextSpawnRoll = now.Add(time.Duration(m.randFloat() * float64(interval)))
		return nil
	}
	if now.Before(m.nextSpawnRoll) {
		return nil
	}
	m.nextSpawnRoll = now.Add(time.Duration((0.5 + m.randFloat()) * float64(interval)))

	if !gc.GetTrickOrTreatEnabled() {
		return nil
	}
	channels := gc.GetTrickOrTreatChannels()
	if len(channels) == 0 {
		return nil
	}
	active, err := m.store.ActiveBowlChannels(gc.GuildID())
	if err != nil {
		return err
	}
	var errs []error
	for _, ch := range channels {
		if active[ch] {
			continue
		}
		chance := 0.10 + 0.05*m.randFloat()
		if m.randFloat() >= chance {
			continue
		}
		if _, err := m.spawnBowl(m.session, gc.GuildID(), ch); err != nil {
			errs = append(errs, fmt.Errorf("channel %s: %w", ch, err))
		}
	}
	return errors.Join(errs...)
}

// expireTick archives bowls whose TRICK window has ended.
func (m *Module) expireTick() error {
	if m.store == nil || m.session == nil {
		return nil
	}
	bowls, err := m.store.ExpiredBowls(m.now())
	if err != nil {
		return err
	}
	for _, b := range bowls {
		lock := m.bowlLock(b.MessageID)
		lock.Lock()
		err := m.archiveBowl(m.session, b.MessageID)
		lock.Unlock()
		if err != nil {
			// The bowl stays archived even if its message couldn't be edited;
			// a later click on it clears the buttons.
			m.config.Logger.Warnf("trick-or-treat: failed to update archived bowl %s: %v", b.MessageID, err)
		}
	}
	return nil
}

// Service drives spawning and expiry from the scheduler.
type Service struct {
	m *Module
}

// HydrateServiceDiscordSession stores the session and archives any bowls that
// expired while the bot was down.
func (s *Service) HydrateServiceDiscordSession(session *discordgo.Session) error {
	s.m.session = session
	go func() {
		if err := s.m.expireTick(); err != nil {
			s.m.config.Logger.Errorf("trick-or-treat: startup expiry failed: %v", err)
		}
	}()
	return nil
}

// ScheduledFuncs checks for spawns and expired bowls every minute.
func (s *Service) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@every 1m": func() error {
			return errors.Join(s.m.expireTick(), s.m.spawnTick())
		},
	}
}
