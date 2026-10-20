package score

import (
	"errors"
	"fmt"
	"math/rand/v2"
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
	// leaderboardSize is how many users /leaderboard lists.
	leaderboardSize = 10
)

// store is the persistence the module needs; *database.DB satisfies it.
type store interface {
	GiveScoreItem(guildID, userID, name string, count int64) (database.GiveResult, error)
	TakeScoreItem(guildID, userID, name string, count int64) (database.TakeResult, int64, error)
	TakeAllScoreItem(guildID, userID, name string) (database.TakeResult, int64, error)
	SuggestScoreItemNames(guildID, userID, query string) ([]string, error)
	ListScoreItemNames(guildID string, limit int) ([]string, error)
	GetScoreItems(guildID, userID string) ([]database.ScoreItem, error)
	GetScoreItemTotals(guildID string) ([]database.ScoreItem, error)
	GetScoreLeaderboard(guildID, thing string, limit int) (string, []database.ScoreLeaderboardEntry, error)
	GetScoreRank(guildID, thing, userID string) (int64, int, error)
	PurgeScoreMember(guildID, userID string) error
	RenameScoreItem(guildID, from, to string) (database.RenameResult, int, error)
	WipeScoreItem(guildID, thing string) (int, error)
}

// discordOps wraps the Discord calls the handlers make so tests can capture them.
type discordOps struct {
	Respond      func(s *discordgo.Session, i *discordgo.Interaction, resp *discordgo.InteractionResponse) error
	EditResponse func(s *discordgo.Session, i *discordgo.Interaction, edit *discordgo.WebhookEdit) error
	SendMessage  func(s *discordgo.Session, channelID string, msg *discordgo.MessageSend) error
	// IsMember reports whether a user is still in a guild.
	IsMember func(s *discordgo.Session, guildID, userID string) (bool, error)
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
		IsMember: func(s *discordgo.Session, guildID, userID string) (bool, error) {
			if s == nil {
				return true, nil
			}
			if s.State != nil {
				if _, err := s.State.Member(guildID, userID); err == nil {
					return true, nil
				}
			}
			member, err := s.GuildMember(guildID, userID)
			var restErr *discordgo.RESTError
			if errors.As(err, &restErr) && restErr.Message != nil && restErr.Message.Code == discordgo.ErrCodeUnknownMember {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			// Cache it so the next leaderboard doesn't ask again.
			if s.State != nil {
				member.GuildID = guildID
				_ = s.State.MemberAdd(member)
			}
			return true, nil
		},
	}
}

// Module implements /give and /take (moderators hand out and remove arbitrary
// things), /score (anyone lists what a user holds) and /leaderboard (anyone
// ranks who holds the most of a thing). /things pulls random things out of a
// hat. /managethings lets moderators rename or wipe a thing server-wide.
type Module struct {
	config *config.Config
	store  store
	ops    discordOps
	// randN returns a uniform random number in [0, n); swapped out in tests.
	randN func(n int64) int64
	// session is used by agent tools, which run outside an interaction.
	session *discordgo.Session
}

// New creates a new score module
func New(deps *types.Dependencies) *Module {
	m := &Module{config: deps.Config, ops: defaultDiscordOps(), randN: rand.Int64N, session: deps.Session}
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
				Type:         discordgo.ApplicationCommandOptionString,
				Name:         "thing",
				Description:  thingDesc,
				Required:     true,
				MaxLength:    maxThingLength,
				Autocomplete: true,
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

// Register adds the /give, /take, /managethings, /things, /score and /leaderboard commands to the command map
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
	cmds["take"].ApplicationCommand.Options = append(cmds["take"].ApplicationCommand.Options, &discordgo.ApplicationCommandOption{
		Type:        discordgo.ApplicationCommandOptionBoolean,
		Name:        "all",
		Description: "Take every one they have (instead of a count)",
		Required:    false,
	})

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

	cmds["leaderboard"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "leaderboard",
			Description: "See who has the most of something",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:         discordgo.ApplicationCommandOptionString,
					Name:         "thing",
					Description:  "What to rank (e.g. horses)",
					Required:     true,
					MaxLength:    maxThingLength,
					Autocomplete: true,
				},
			},
		},
		HandlerFunc: m.handleLeaderboard,
	}

	cmds["things"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "things",
			Description: "Pull 3 random things out of the hat",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleHat,
	}

	var modPerms int64 = discordgo.PermissionBanMembers
	thingOption := func(desc string) *discordgo.ApplicationCommandOption {
		return &discordgo.ApplicationCommandOption{
			Type:         discordgo.ApplicationCommandOptionString,
			Name:         "thing",
			Description:  desc,
			Required:     true,
			MaxLength:    maxThingLength,
			Autocomplete: true,
		}
	}
	cmds["managethings"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "managethings",
			Description:              "Clean up a thing for everyone",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "rename",
					Description: "Rename a thing for everyone who has it (merges into an existing thing)",
					Options: []*discordgo.ApplicationCommandOption{
						thingOption("The thing to rename"),
						{
							Type:        discordgo.ApplicationCommandOptionString,
							Name:        "to",
							Description: "The new name",
							Required:    true,
							MaxLength:   maxThingLength,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "wipe",
					Description: "Remove a thing from everyone, as if it was never given",
					Options:     []*discordgo.ApplicationCommandOption{thingOption("The thing to wipe")},
				},
			},
		},
		HandlerFunc: m.handleThings,
	}
}

// Service returns nil as this module has no services requiring initialization
func (m *Module) Service() types.ModuleService {
	return nil
}

func (m *Module) handleGive(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleChange(s, i, "give", func(userID, thing string, count int64, _ bool) (string, string, error) {
		result, err := m.store.GiveScoreItem(i.GuildID, userID, thing, count)
		if err != nil || result == database.GiveOverflow {
			return "", "❌ That would be too many. Nothing was given.", err
		}
		return fmt.Sprintf("<@%s> has earned %s!", userID, formatThing(thing, count)), "", nil
	})
}

func (m *Module) handleTake(s *discordgo.Session, i *discordgo.InteractionCreate) {
	m.handleChange(s, i, "take", func(userID, thing string, count int64, all bool) (string, string, error) {
		var result database.TakeResult
		var held int64
		var err error
		if all {
			result, held, err = m.store.TakeAllScoreItem(i.GuildID, userID, thing)
			count = held
		} else {
			result, held, err = m.store.TakeScoreItem(i.GuildID, userID, thing, count)
		}
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
type changeFunc func(userID, thing string, count int64, all bool) (announcement, note string, err error)

// handleChange runs the flow shared by /give and /take: validate, make sure
// the announcement can be posted, apply the change, then announce it as a
// plain channel message so nothing shows which moderator ran the command.
func (m *Module) handleChange(s *discordgo.Session, i *discordgo.InteractionCreate, verb string, apply changeFunc) {
	var userID, thing string
	var count int64 = 1
	var countSet, all bool
	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "user":
			userID = opt.UserValue(nil).ID
		case "thing":
			thing = database.NormalizeScoreItemName(opt.StringValue())
		case "count":
			count = opt.IntValue()
			countSet = true
		case "all":
			all = opt.BoolValue()
		}
	}

	if userID == "" || thing == "" {
		m.respondEphemeral(s, i, "❌ Please specify a user and a thing.")
		return
	}
	if all && countSet {
		m.respondEphemeral(s, i, "❌ Use either a count or all, not both.")
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

	announcement, note, err := apply(userID, thing, count, all)
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

// HandleAutocomplete suggests things for the /give, /take, /managethings and
// /leaderboard thing option. /take (and /give once a user is picked) suggests what that
// user has; otherwise anything held in the server is suggested. Only things someone
// currently holds are suggested.
func (m *Module) HandleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	var query, userID string
	focusedThing := false
	opts := data.Options
	if len(opts) == 1 && opts[0].Type == discordgo.ApplicationCommandOptionSubCommand {
		opts = opts[0].Options
	}
	for _, opt := range opts {
		switch opt.Name {
		case "thing":
			query, _ = opt.Value.(string)
			focusedThing = opt.Focused
		case "user":
			// Unresolved in autocomplete: the value is the raw user ID.
			userID, _ = opt.Value.(string)
		}
	}

	choices := []*discordgo.ApplicationCommandOptionChoice{}
	if focusedThing && m.store != nil {
		names, err := m.suggest(i.GuildID, data.Name, userID, query)
		if err != nil {
			m.config.Logger.Errorf("%s: autocomplete failed: %v", data.Name, err)
		}
		for _, name := range names {
			choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: name})
		}
	}

	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}); err != nil {
		m.config.Logger.Errorf("%s: failed to respond to autocomplete: %v", data.Name, err)
	}
}

func (m *Module) suggest(guildID, command, userID, query string) ([]string, error) {
	if userID == "" || command == "give" {
		// A give can stack onto anything that exists in the server, but the
		// recipient's own things are listed first.
		var own []string
		if userID != "" {
			var err error
			if own, err = m.store.SuggestScoreItemNames(guildID, userID, query); err != nil {
				return nil, err
			}
		}
		all, err := m.store.SuggestScoreItemNames(guildID, "", query)
		if err != nil {
			return nil, err
		}
		return mergeSuggestions(own, all), nil
	}
	return m.store.SuggestScoreItemNames(guildID, userID, query)
}

// mergeSuggestions appends rest to first, skipping case-insensitive
// duplicates and capping at Discord's 25-choice limit.
func mergeSuggestions(first, rest []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, name := range append(first, rest...) {
		key := strings.ToLower(name)
		if seen[key] {
			continue
		}
		if len(out) == 25 {
			break
		}
		seen[key] = true
		out = append(out, name)
	}
	return out
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

func (m *Module) handleLeaderboard(s *discordgo.Session, i *discordgo.InteractionCreate) {
	var thing string
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "thing" {
			thing = database.NormalizeScoreItemName(opt.StringValue())
		}
	}
	if thing == "" {
		m.respondEphemeral(s, i, "❌ Please specify a thing.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}

	callerID := invokerID(i)

	name, entries, err := m.leaderboard(s, i.GuildID, thing)
	if err != nil {
		m.config.Logger.Errorf("leaderboard: failed to load %q: %v", thing, err)
		m.respondEphemeral(s, i, "❌ Failed to load the leaderboard.")
		return
	}
	if name == "" {
		name = thing
	}

	var caller *rankedEntry
	if callerID != "" && len(entries) > 0 && !containsUser(entries, callerID) {
		count, rank, err := m.store.GetScoreRank(i.GuildID, thing, callerID)
		if err != nil {
			m.config.Logger.Errorf("leaderboard: failed to rank user %s: %v", callerID, err)
		} else if count > 0 {
			caller = &rankedEntry{rank: rank, ScoreLeaderboardEntry: database.ScoreLeaderboardEntry{UserID: callerID, Count: count}}
		}
	}

	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: buildLeaderboardMessage(name, entries, caller),
			// Show the mentions without pinging anyone.
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	}); err != nil {
		m.config.Logger.Errorf("leaderboard: failed to respond: %v", err)
	}
}

// maxLeaderboardRechecks bounds how many times the leaderboard is reloaded
// after finding holders who left, keeping the number of Discord lookups small.
const maxLeaderboardRechecks = 3

// leaderboard loads the top holders of a thing, confirming each is still in
// the server. Anyone found to have left has their things purged and the list
// is reloaded. This catches
// departures the member-remove event missed, e.g. while the bot was offline.
func (m *Module) leaderboard(s *discordgo.Session, guildID, thing string) (string, []database.ScoreLeaderboardEntry, error) {
	for attempt := 0; ; attempt++ {
		name, entries, err := m.store.GetScoreLeaderboard(guildID, thing, leaderboardSize)
		if err != nil {
			return "", nil, err
		}
		var present []database.ScoreLeaderboardEntry
		for _, e := range entries {
			member, err := m.ops.IsMember(s, guildID, e.UserID)
			if err != nil {
				// Don't hide someone over a transient lookup failure.
				m.config.Logger.Warnf("leaderboard: couldn't check membership of %s: %v", e.UserID, err)
				member = true
			}
			if member {
				present = append(present, e)
				continue
			}
			if err := m.store.PurgeScoreMember(guildID, e.UserID); err != nil {
				return "", nil, err
			}
		}
		if len(present) == len(entries) || attempt == maxLeaderboardRechecks {
			return name, present, nil
		}
	}
}

func invokerID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	return ""
}

func containsUser(entries []database.ScoreLeaderboardEntry, userID string) bool {
	for _, e := range entries {
		if e.UserID == userID {
			return true
		}
	}
	return false
}

type rankedEntry struct {
	database.ScoreLeaderboardEntry
	rank int
}

// buildLeaderboardMessage ranks holders of a thing. Tied counts share a rank
// (1, 1, 3). caller, when set, is the invoker's own spot below the top list.
// At most leaderboardSize+1 short lines, so it always fits a message.
func buildLeaderboardMessage(name string, entries []database.ScoreLeaderboardEntry, caller *rankedEntry) string {
	if len(entries) == 0 {
		return fmt.Sprintf("Nobody has any %s.", name)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Leaderboard for %s:\n", name)
	rank := 0
	for idx, e := range entries {
		if idx == 0 || e.Count != entries[idx-1].Count {
			rank = idx + 1
		}
		writeLeaderboardLine(&b, rank, e)
	}
	if caller != nil {
		b.WriteString("\n…")
		writeLeaderboardLine(&b, caller.rank, caller.ScoreLeaderboardEntry)
	}
	return b.String()
}

// writeLeaderboardLine escapes the rank's dot so Discord doesn't render the
// lines as a Markdown list, which would renumber tied ranks.
func writeLeaderboardLine(b *strings.Builder, rank int, e database.ScoreLeaderboardEntry) {
	fmt.Fprintf(b, "\n%d\\. <@%s> — %d", rank, e.UserID, e.Count)
}

// handleThings runs /managethings rename and /managethings wipe. These are cleanup tools,
// so the result goes only to the moderator and nothing is announced.
func (m *Module) handleThings(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	if len(options) != 1 {
		m.respondEphemeral(s, i, "❌ Please pick rename or wipe.")
		return
	}
	sub := options[0]
	var thing, to string
	for _, opt := range sub.Options {
		switch opt.Name {
		case "thing":
			thing = database.NormalizeScoreItemName(opt.StringValue())
		case "to":
			to = database.NormalizeScoreItemName(opt.StringValue())
		}
	}
	if thing == "" || (sub.Name == "rename" && to == "") {
		m.respondEphemeral(s, i, "❌ Please specify a thing.")
		return
	}
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}

	switch sub.Name {
	case "rename":
		result, people, err := m.store.RenameScoreItem(i.GuildID, thing, to)
		switch {
		case err != nil:
			m.config.Logger.Errorf("things: failed to rename %q to %q: %v", thing, to, err)
			m.respondEphemeral(s, i, "❌ Failed to rename. Nothing changed.")
		case result == database.RenameNotHeld:
			m.respondEphemeral(s, i, fmt.Sprintf("ℹ️ Nobody has any %s. Nothing changed.", thing))
		case result == database.RenameOverflow:
			m.respondEphemeral(s, i, "❌ Merging would give someone too many. Nothing changed.")
		default:
			m.respondEphemeral(s, i, fmt.Sprintf("✅ Renamed %s to %s for %s.", thing, to, peopleCount(people)))
		}
	case "wipe":
		people, err := m.store.WipeScoreItem(i.GuildID, thing)
		switch {
		case err != nil:
			m.config.Logger.Errorf("things: failed to wipe %q: %v", thing, err)
			m.respondEphemeral(s, i, "❌ Failed to wipe. Nothing changed.")
		case people == 0:
			m.respondEphemeral(s, i, fmt.Sprintf("ℹ️ Nobody has any %s. Nothing changed.", thing))
		default:
			m.respondEphemeral(s, i, fmt.Sprintf("✅ Wiped %s from %s.", thing, peopleCount(people)))
		}
	default:
		m.respondEphemeral(s, i, "❌ Unknown subcommand.")
	}
}

// hatPulls is how many things /things pulls out of the hat.
const hatPulls = 3

// handleHat runs /things: it pulls hatPulls things out of a hat holding every
// thing anyone in the server has, each with an equal chance no matter how many
// are held. Pulls that land on the same thing are shown once.
func (m *Module) handleHat(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if m.store == nil {
		m.respondEphemeral(s, i, "❌ Database is unavailable.")
		return
	}
	totals, err := m.store.GetScoreItemTotals(i.GuildID)
	if err != nil {
		m.config.Logger.Errorf("things: failed to load the hat: %v", err)
		m.respondEphemeral(s, i, "❌ Failed to reach into the hat.")
		return
	}
	pulled := pullFromHat(totals, hatPulls, m.randN)
	if len(pulled) == 0 {
		m.respondEphemeral(s, i, "🎩 The hat is empty. Nobody has anything yet.")
		return
	}
	if err := m.ops.Respond(s, i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:         "🎩 You reach into the hat and pull out: " + strings.Join(pulled, ", "),
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		},
	}); err != nil {
		m.config.Logger.Errorf("things: failed to respond: %v", err)
	}
}

// pullFromHat draws pulls times from a hat holding one slip per distinct
// thing, putting each slip back after it's drawn so every thing has an equal
// chance on every pull, and returns the distinct things drawn in draw order.
func pullFromHat(totals []database.ScoreItem, pulls int, randN func(int64) int64) []string {
	var things []string
	for _, t := range totals {
		if t.Count > 0 {
			things = append(things, t.Name)
		}
	}
	if len(things) == 0 {
		return nil
	}

	seen := map[int64]bool{}
	var names []string
	for range pulls {
		idx := randN(int64(len(things)))
		if !seen[idx] {
			seen[idx] = true
			names = append(names, things[idx])
		}
	}
	return names
}

func peopleCount(n int) string {
	if n == 1 {
		return "1 person"
	}
	return fmt.Sprintf("%d people", n)
}

// OnGuildMemberRemove purges everything a departing member holds.
func (m *Module) OnGuildMemberRemove(_ *discordgo.Session, e *discordgo.GuildMemberRemove) {
	if m.store == nil || e.User == nil {
		return
	}
	if err := m.store.PurgeScoreMember(e.GuildID, e.User.ID); err != nil {
		m.config.Logger.Errorf("score: failed to purge member %s: %v", e.User.ID, err)
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
