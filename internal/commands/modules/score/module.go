package score

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

const (
	maxThingLength = 100
	maxGiveCount   = 1_000_000
	// Discord rejects message content longer than 2000 characters.
	maxMessageLength = 2000
)

// store is the persistence the module needs; *database.DB satisfies it.
type store interface {
	GiveScoreItem(guildID, userID, name string, count *int64) (database.GiveResult, error)
	GetScoreItems(guildID, userID string) ([]database.ScoreItem, error)
}

// discordOps wraps the Discord calls the handlers make so tests can capture them.
type discordOps struct {
	Respond      func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error
	EditResponse func(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error
	SendMessage  func(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) error
}

func defaultDiscordOps() discordOps {
	return discordOps{
		Respond: func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error {
			return s.InteractionRespond(i, resp)
		},
		EditResponse: func(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error {
			_, err := s.InteractionResponseEdit(i, edit)
			return err
		},
		SendMessage: func(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) error {
			_, err := s.ChannelMessageSendComplex(channelID, msg)
			return err
		},
	}
}

// Module implements /give (moderators hand out arbitrary things) and /score
// (anyone lists what a user holds).
type Module struct {
	config *config.Config
	store  store
	ops    discordOps
}

// New creates a new score module
func New(deps *types.Dependencies) *Module {
	m := &Module{config: deps.Config, ops: defaultDiscordOps()}
	// Assign only a non-nil DB so the nil check in handlers sees a nil interface.
	if deps.DB != nil {
		m.store = deps.DB
	}
	return m
}

// Register adds the /give and /score commands to the command map
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var modPerms int64 = discordgo.PermissionBanMembers
	guildOnly := &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild}
	minCount := 1.0

	cmds["give"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "give",
			Description:              "Give a user something",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "Who receives it",
					Required:    true,
				},
				{
					Type:        discordgo.ApplicationCommandOptionString,
					Name:        "thing",
					Description: "What they receive, exactly as it should be shown (e.g. horses, a can of eggs)",
					Required:    true,
					MaxLength:   maxThingLength,
				},
				{
					Type:        discordgo.ApplicationCommandOptionInteger,
					Name:        "count",
					Description: "How many (omit for a one-off thing shown as-is)",
					Required:    false,
					MinValue:    &minCount,
					MaxValue:    maxGiveCount,
				},
			},
		},
		HandlerFunc: m.handleGive,
	}

	cmds["score"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "score",
			Description: "See what someone has",
			Contexts:    guildOnly,
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "user",
					Description: "Whose score to show (defaults to you)",
					Required:    false,
				},
			},
		},
		HandlerFunc: m.handleScore,
	}
}

// Service returns nil as this module has no services requiring initialization
func (m *Module) Service() types.ModuleService {
	return nil
}

func (m *Module) handleGive(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var userID, thing string
	var count *int64
	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "user":
			userID = opt.UserValue(nil).ID
		case "thing":
			thing = database.NormalizeScoreItemName(opt.StringValue())
		case "count":
			c := opt.IntValue()
			count = &c
		}
	}

	if userID == "" || thing == "" {
		m.respondEphemeral(s, i, "❌ Please specify a user and a thing.")
		return
	}
	if count != nil && (*count < 1 || *count > maxGiveCount) {
		m.respondEphemeral(s, i, fmt.Sprintf("❌ Count must be between 1 and %d.", maxGiveCount))
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	// Check before saving so a give is never recorded without its announcement.
	if !botCanPost(s, i) {
		m.respondEphemeral(s, i, "❌ I can't post in this channel, so nothing was given.")
		return
	}

	// Defer ephemerally so only the moderator sees the acknowledgement; the
	// public announcement is a plain channel message with no interaction
	// attribution.
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		m.config.Logger.Errorf("give: failed to defer interaction: %v", err)
		return
	}

	result, err := m.store.GiveScoreItem(i.GuildID, userID, thing, count)
	if err != nil {
		m.config.Logger.Errorf("give: failed to save %q for user %s: %v", thing, userID, err)
		m.editResponse(s, i, "❌ Failed to save. Nothing was announced.")
		return
	}
	switch result {
	case database.GiveAlreadyHeld:
		m.editResponse(s, i, fmt.Sprintf("ℹ️ <@%s> already has %s. Nothing was announced.", userID, thing))
		return
	case database.GiveKindMismatch:
		msg := fmt.Sprintf("❌ <@%s> already has %q as a one-off thing, so give it without a count. Nothing was announced.", userID, thing)
		if count == nil {
			msg = fmt.Sprintf("❌ <@%s> already has a counted %q, so give it with a count. Nothing was announced.", userID, thing)
		}
		m.editResponse(s, i, msg)
		return
	}

	err = m.ops.SendMessage(s, i.ChannelID, &discordgo.MessageSend{
		Content: fmt.Sprintf("<@%s> has earned %s!", userID, formatThing(thing, count)),
		// Only the recipient may be pinged, whatever the thing's text contains.
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{userID}},
	})
	if err != nil {
		m.config.Logger.Errorf("give: saved but failed to announce in channel %s: %v", i.ChannelID, err)
		m.editResponse(s, i, "⚠️ Saved, but I couldn't post the announcement in this channel.")
		return
	}

	m.editResponse(s, i, "✅ Done.")
}

func (m *Module) handleScore(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var userID string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "user" {
			userID = opt.UserValue(nil).ID
		}
	}
	if userID == "" && i.Member != nil && i.Member.User != nil {
		userID = i.Member.User.ID
	}
	if userID == "" {
		m.respondEphemeral(s, i, "❌ Couldn't tell whose score to show.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}

	items, err := m.store.GetScoreItems(i.GuildID, userID)
	if err != nil {
		m.config.Logger.Errorf("score: failed to load items for user %s: %v", userID, err)
		m.respondEphemeral(s, i, "❌ Failed to load the score.")
		return
	}

	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: buildScoreMessage(userID, items),
			// Show the mention without pinging anyone.
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	}); err != nil {
		m.config.Logger.Errorf("score: failed to respond: %v", err)
	}
}

// botCanPost reports whether the bot may post in the invoking channel, using
// the permissions Discord computed for this interaction (correct for threads,
// unlike computing them from cached overwrites).
func botCanPost(s *discordgo.Session, i *discordgo.InteractionCreate) bool {
	sendBit := int64(discordgo.PermissionSendMessages)
	if s != nil && s.State != nil {
		if ch, err := s.State.Channel(i.ChannelID); err == nil && ch.IsThread() {
			sendBit = discordgo.PermissionSendMessagesInThreads
		}
	}
	need := discordgo.PermissionViewChannel | sendBit
	return i.AppPermissions&need == need
}

// formatThing renders a thing as announced and listed: "24 horses" when
// counted, or the name as-is ("a can of eggs") when not.
func formatThing(name string, count *int64) string {
	if count == nil {
		return name
	}
	return fmt.Sprintf("%d %s", *count, name)
}

// buildScoreMessage lists a user's things, truncating to fit Discord's message
// length limit.
func buildScoreMessage(userID string, items []database.ScoreItem) string {
	if len(items) == 0 {
		return fmt.Sprintf("<@%s> has nothing.", userID)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "<@%s> has:\n", userID)
	// Discord's limit counts characters, not bytes.
	length := utf8.RuneCountInString(b.String())
	for idx, item := range items {
		line := "\n- " + formatThing(item.Name, item.Count)
		lineLen := utf8.RuneCountInString(line)
		need := lineLen
		// Keep room for the overflow note in case the following lines don't fit.
		if rest := len(items) - idx - 1; rest > 0 {
			need += utf8.RuneCountInString(moreNote(rest))
		}
		if length+need > maxMessageLength {
			b.WriteString(moreNote(len(items) - idx))
			break
		}
		b.WriteString(line)
		length += lineLen
	}
	return b.String()
}

func moreNote(n int) string {
	return fmt.Sprintf("\n- …and %d more", n)
}

func (m *Module) respondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: content, Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		m.config.Logger.Errorf("score: failed to respond: %v", err)
	}
}

func (m *Module) editResponse(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	if err := m.ops.EditResponse(s, i.Interaction, &discordgo.WebhookEdit{Content: &content}); err != nil {
		m.config.Logger.Errorf("give: failed to edit response: %v", err)
	}
}
