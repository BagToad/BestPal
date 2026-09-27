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
	// Discord rejects message content longer than 2000 characters.
	maxMessageLength = 2000
)

// store is the persistence the module needs; *database.DB satisfies it.
type store interface {
	GiveScoreItem(guildID, userID, name string, count int64) (database.GiveResult, error)
	TakeScoreItem(guildID, userID, name string, count int64) (database.TakeResult, int64, error)
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

// Module implements /give and /take (moderators hand out and remove arbitrary
// things) and /score (anyone lists what a user holds).
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

func modCommand(name, description, userDesc, thingDesc, countDesc string) *discordgo.ApplicationCommand {
	var modPerms int64 = discordgo.PermissionBanMembers
	minCount := 1.0
	return &discordgo.ApplicationCommand{
		Name:                     name,
		Description:              description,
		DefaultMemberPermissions: &modPerms,
		Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		Options: []*discordgo.ApplicationCommandOption{
			{
				Type:        discordgo.ApplicationCommandOptionUser,
				Name:        "user",
				Description: userDesc,
				Required:    true,
			},
			{
				Type:        discordgo.ApplicationCommandOptionString,
				Name:        "thing",
				Description: thingDesc,
				Required:    true,
				MaxLength:   maxThingLength,
			},
			{
				// No MaxValue: Discord's own integer limit (2^53) is the cap.
				Type:        discordgo.ApplicationCommandOptionInteger,
				Name:        "count",
				Description: countDesc,
				Required:    false,
				MinValue:    &minCount,
			},
		},
	}
}

// Register adds the /give, /take and /score commands to the command map
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["give"] = &types.Command{
		ApplicationCommand: modCommand("give", "Give a user something",
			"Who receives it", "What they receive (e.g. horses)", "How many (defaults to 1)"),
		HandlerFunc: m.handleGive,
	}
	cmds["take"] = &types.Command{
		ApplicationCommand: modCommand("take", "Take something from a user",
			"Who loses it", "What they lose (e.g. horses)", "How many (defaults to 1)"),
		HandlerFunc: m.handleTake,
	}

	cmds["score"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "score",
			Description: "See what someone has",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
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
	m.handleChange(s, i, "give", func(userID, thing string, count int64) (string, string, error) {
		result, err := m.store.GiveScoreItem(i.GuildID, userID, thing, count)
		if err != nil || result == database.GiveOverflow {
			return "", "❌ That would be too many. Nothing was given.", err
		}
		return fmt.Sprintf("<@%s> has earned %s!", userID, formatThing(thing, count)), "", nil
	})
}

func (m *Module) handleTake(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleChange(s, i, "take", func(userID, thing string, count int64) (string, string, error) {
		result, held, err := m.store.TakeScoreItem(i.GuildID, userID, thing, count)
		switch {
		case err != nil:
			return "", "", err
		case result == database.TakeNotHeld:
			return "", fmt.Sprintf("ℹ️ <@%s> doesn't have any %s. Nothing was taken.", userID, thing), nil
		case result == database.TakeInsufficient:
			return "", fmt.Sprintf("ℹ️ <@%s> only has %s. Nothing was taken.", userID, formatThing(thing, held)), nil
		}
		return fmt.Sprintf("<@%s> has lost %s!", userID, formatThing(thing, count)), "", nil
	})
}

// changeFunc applies a give or take. It returns the public announcement, or a
// private note for the moderator when nothing changed.
type changeFunc func(userID, thing string, count int64) (announcement, note string, err error)

// handleChange runs the flow shared by /give and /take: validate, make sure
// the announcement can be posted, apply the change, then announce it as a
// plain channel message so nothing shows which moderator ran the command.
func (m *Module) handleChange(s *discordgo.Session, i *discordgo.InteractionCreate, verb string, apply changeFunc) {
	var userID, thing string
	var count int64 = 1
	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "user":
			userID = opt.UserValue(nil).ID
		case "thing":
			thing = database.NormalizeScoreItemName(opt.StringValue())
		case "count":
			count = opt.IntValue()
		}
	}

	if userID == "" || thing == "" {
		m.respondEphemeral(s, i, "❌ Please specify a user and a thing.")
		return
	}
	if count < 1 {
		m.respondEphemeral(s, i, "❌ Count must be at least 1.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	// Check before saving so a change is never recorded without its announcement.
	if !botCanPost(s, i) {
		m.respondEphemeral(s, i, "❌ I can't post in this channel, so nothing changed.")
		return
	}

	// Defer ephemerally so only the moderator sees the acknowledgement.
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		m.config.Logger.Errorf("%s: failed to defer interaction: %v", verb, err)
		return
	}

	announcement, note, err := apply(userID, thing, count)
	if err != nil {
		m.config.Logger.Errorf("%s: failed to save %q for user %s: %v", verb, thing, userID, err)
		m.editResponse(s, i, "❌ Failed to save. Nothing was announced.")
		return
	}
	if announcement == "" {
		m.editResponse(s, i, note)
		return
	}

	err = m.ops.SendMessage(s, i.ChannelID, &discordgo.MessageSend{
		Content: announcement,
		// Only the recipient may be pinged, whatever the thing's text contains.
		AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{userID}},
	})
	if err != nil {
		m.config.Logger.Errorf("%s: saved but failed to announce in channel %s: %v", verb, i.ChannelID, err)
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

// formatThing renders a thing as announced and listed, e.g. "24 horses".
func formatThing(name string, count int64) string {
	return fmt.Sprintf("%d %s", count, name)
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
		m.config.Logger.Errorf("score: failed to edit response: %v", err)
	}
}
