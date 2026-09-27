package score

import (
	"fmt"
	"strings"

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
	GiveScoreItem(guildID, userID, name string, count *int64) error
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

	if err := m.store.GiveScoreItem(i.GuildID, userID, thing, count); err != nil {
		m.config.Logger.Errorf("give: failed to save %q for user %s: %v", thing, userID, err)
		m.editResponse(s, i, "❌ Failed to save. Nothing was announced.")
		return
	}

	err := m.ops.SendMessage(s, i.ChannelID, &discordgo.MessageSend{
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
	for idx, item := range items {
		line := "\n- " + formatThing(item.Name, item.Count)
		need := len(line)
		// Keep room for the overflow note in case the following lines don't fit.
		if rest := len(items) - idx - 1; rest > 0 {
			need += len(moreNote(rest))
		}
		if b.Len()+need > maxMessageLength {
			b.WriteString(moreNote(len(items) - idx))
			break
		}
		b.WriteString(line)
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
