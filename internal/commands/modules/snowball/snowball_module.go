package snowball

import (
	"bytes"
	_ "embed"
	"fmt"
	"math/rand/v2"
	"sort"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

//go:embed snowfall.gif
var snowfallGIF []byte

type snowfallState struct {
	Active       bool
	ChannelID    string
	EndsAt       time.Time
	ThrowsByUser map[string]int
	HitsByUser   map[string]int
	HitsOnUser   map[string]int
}

// SnowballModule implements the CommandModule interface for snowball commands
type SnowballModule struct {
	config *config.Config
	db     *database.DB

	state   snowfallState
	stateMu sync.Mutex
}

// New creates a new snowball module
func New(deps *types.Dependencies) *SnowballModule {
	return &SnowballModule{
		config: deps.Config,
		db:     deps.DB,
		state: snowfallState{
			Active:       false,
			ChannelID:    "",
			ThrowsByUser: make(map[string]int),
			HitsByUser:   make(map[string]int),
			HitsOnUser:   make(map[string]int),
		},
	}
}

// Register adds snowball commands to the command map
func (m *SnowballModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	var adminPerms int64 = discordgo.PermissionAdministrator
	var modPerms int64 = discordgo.PermissionBanMembers

	cmds["snowfall"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "snowfall",
			Description:              "Start or stop a festive snowfall",
			DefaultMemberPermissions: &modPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "start",
					Description: "Start a snowfall in a channel",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionChannel,
							Name:        "channel",
							Description: "Channel where it will snow",
							Required:    true,
						},
						{
							Type:        discordgo.ApplicationCommandOptionInteger,
							Name:        "minutes",
							Description: "How long the snowfall should last",
							Required:    true,
							MinValue:    &[]float64{1}[0],
							MaxValue:    180,
						},
					},
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "stop",
					Description: "Stop the current snowfall",
					Options: []*discordgo.ApplicationCommandOption{
						{
							Type:        discordgo.ApplicationCommandOptionChannel,
							Name:        "channel",
							Description: "Channel where snowfall is happening",
							Required:    true,
						},
					},
				},
			},
		},
		HandlerFunc: m.handleSnowfall,
	}

	cmds["snowball"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "snowball",
			Description: "Throw a snowball at someone while it is snowing",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionUser,
					Name:        "target",
					Description: "Who you want to pelt",
					Required:    true,
				},
			},
		},
		HandlerFunc: m.handleSnowball,
	}

	// User context (right-click) command to quickly throw a snowball at a selected user.
	cmds["Throw snowball"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name: "Throw snowball",
			Type: discordgo.UserApplicationCommand,
		},
		HandlerFunc: m.handleSnowballUserContext,
	}

	cmds["snowball-score"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "snowball-score",
			Description: "View the snowball leaderboard",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleSnowballScore,
	}

	cmds["snowball-reset"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:                     "snowball-reset",
			Description:              "Reset the snowball leaderboard for this server",
			DefaultMemberPermissions: &adminPerms,
			Contexts:                 &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleSnowballReset,
	}
}

// Service returns nil as snowball currently has no background service
func (m *SnowballModule) Service() types.ModuleService { return nil }

func (m *SnowballModule) handleSnowfall(s *discordgo.Session, i *discordgo.InteractionCreate) {
	options := i.ApplicationCommandData().Options
	if len(options) == 0 {
		return
	}

	sub := options[0]
	switch sub.Name {
	case "start":
		m.handleSnowfallStart(s, i, sub)
	case "stop":
		m.handleSnowfallStop(s, i, sub)
	}
}

func (m *SnowballModule) handleSnowfallStart(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	state := m.snowfallState()

	if state.Active {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "It's already snowing somewhere! Use /snowfall stop first.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with already-active snowfall message: %v", err)
		}

		return
	}

	var channelID string
	var minutes int64

	for _, opt := range sub.Options {
		if opt.Name == "channel" && opt.ChannelValue(s) != nil {
			channelID = opt.ChannelValue(s).ID
		}
		if opt.Name == "minutes" && opt.IntValue() > 0 {
			minutes = opt.IntValue()
		}
	}

	if channelID == "" || minutes <= 0 {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Please provide a valid channel and duration.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with invalid-parameters message: %v", err)
		}

		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Flags:   discordgo.MessageFlagsEphemeral,
			Content: "Requested a snowfall",
		},
	})
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to respond with snowfall start deferred message: %v", err)
		return
	}

	// log to bestpal log channel
	err = utils.LogToChannel(m.config, s, fmt.Sprintf("❄️ %s requested a snowfall in <#%s> for %d minutes!", i.Member.Mention(), channelID, minutes))
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to log snowfall start to channel: %v", err)
	}

	// Lock to update state, then unlock before Discord API calls
	m.stateMu.Lock()
	m.state = snowfallState{
		Active:       true,
		ChannelID:    channelID,
		EndsAt:       time.Now().Add(time.Duration(minutes) * time.Minute),
		ThrowsByUser: make(map[string]int),
		HitsByUser:   make(map[string]int),
		HitsOnUser:   make(map[string]int),
	}
	m.stateMu.Unlock()

	// Start the auto-stop goroutine immediately after state is set
	// This ensures it runs even if message sending fails below
	go m.autoStopAfterDuration(s)

	var snowfallMsg *discordgo.Message
	if len(snowfallGIF) == 0 {
		var err error
		m.config.Logger.Warn("snowball: embedded snowfall.gif is empty; sending text-only message")
		snowfallMsg, err = s.ChannelMessageSend(channelID, "❄️ It's snowing! Use `/snowball` to join the snowball fight!")
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to send snowfall message: %v", err)
		}
	} else {
		var err error
		snowfallMsg, err = s.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
			Content: "❄️ It's snowing! Use `/snowball` to join the snowball fight!",
			Files: []*discordgo.File{
				{
					Name:   "snowfall.gif",
					Reader: bytes.NewReader(snowfallGIF),
				},
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to send embedded snowfall.gif: %v", err)
		}
	}

	if snowfallMsg == nil || snowfallMsg.ID == "" {
		m.config.Logger.Warn("snowball: snowfall start message failed to send to channel")
	}

	// Updating channel name to indicate snowing
	snowingEmojis := "❄️☃️🌨️"
	channel, err := s.Channel(channelID)
	// Non-fatal if we can't rename the channel.
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to fetch snowfall channel for renaming: %v", err)
	} else if channel != nil {
		newName := strings.TrimSpace(channel.Name + snowingEmojis)
		_, err = s.ChannelEdit(channelID, &discordgo.ChannelEdit{
			Name: newName,
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to rename snowfall channel: %v", err)
		}
	}
}

func (m *SnowballModule) handleSnowfallStop(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	state := m.snowfallState()

	if !state.Active {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "There isn't an active snowfall right now.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with no-active snowfall message: %v", err)
		}
		return
	}

	var channelID string
	for _, opt := range sub.Options {
		if opt.Name == "channel" && opt.ChannelValue(s) != nil {
			channelID = opt.ChannelValue(s).ID
		}
	}

	if channelID == "" || channelID != state.ChannelID {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "That channel doesn't match the active snowfall.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with wrong-channel stop message: %v", err)
		}
		return
	}

	m.postSummaryAndReset(s)

	// Try and remove the snowing emojis from the channel name.
	snowingEmojis := "❄️☃️🌨️"
	channel, err := s.Channel(channelID)
	// Non-fatal if we can't rename the channel.
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to fetch snowfall channel for renaming: %v", err)
	} else if channel != nil {
		newName := strings.TrimSuffix(channel.Name, snowingEmojis)
		_, err = s.ChannelEdit(channelID, &discordgo.ChannelEdit{
			Name: newName,
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to rename snowfall channel: %v", err)
		}
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Snowfall stopped.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to respond with snowfall stop confirmation: %v", err)
	}
}

func (m *SnowballModule) autoStopAfterDuration(s *discordgo.Session) {
	for {
		state := m.snowfallState()

		if !state.Active {
			return
		}
		if time.Now().After(state.EndsAt) {
			m.postSummaryAndReset(s)
			return
		}
		time.Sleep(5 * time.Second)
	}
}

func (m *SnowballModule) handleSnowball(s *discordgo.Session, i *discordgo.InteractionCreate) {
	cmdData := i.ApplicationCommandData()
	var targetUser *discordgo.User
	for _, opt := range cmdData.Options {
		if opt.Name == "target" {
			targetUser = opt.UserValue(s)
			break
		}
	}
	if targetUser == nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Couldn't figure out who you were aiming at. Try using /snowball again and pick a target.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}
	m.throwSnowballAtTarget(s, i, targetUser)
}

// handleSnowballUserContext handles the user context (right-click) "Throw snowball" app.
// It resolves the selected user as the target and then delegates to the same logic
// as the slash command version, with the same rules and messages.
func (m *SnowballModule) handleSnowballUserContext(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	targetID := data.TargetID
	var targetUser *discordgo.User
	if data.Resolved != nil && data.Resolved.Users != nil {
		targetUser = data.Resolved.Users[targetID]
	}
	if targetUser == nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Couldn't figure out who you were aiming at. Try again from the user menu.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	m.throwSnowballAtTarget(s, i, targetUser)
}

// throwSnowballAtTarget contains the shared logic for throwing a snowball from the
// interaction's member at the given target user. It is used by both the /snowball
// slash command and the "Throw snowball" user context command.
func (m *SnowballModule) throwSnowballAtTarget(s *discordgo.Session, i *discordgo.InteractionCreate, targetUser *discordgo.User) {
	state := m.snowfallState()

	if !state.Active {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "It isn't snowing right now, so your snowball just melts in your hands.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with inactive snowfall message: %v", err)
		}
		return
	}

	if state.ChannelID != i.ChannelID {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "It's not snowing here...",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with wrong-channel message: %v", err)
		}
		return
	}

	if i.Member == nil || i.Member.User == nil {
		m.config.Logger.Warn("snowball: interaction missing member or user; ignoring snowball command")
		return
	}

	userID := i.Member.User.ID
	throws := state.ThrowsByUser[userID]

	if throws >= 3 {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You've already thrown 3 snowballs this snowfall. Save some snow for everyone else!",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with max throws message: %v", err)
		}
		return
	}

	if targetUser == nil {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Couldn't figure out who you were aiming at. Try again and pick a target.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with no-target message: %v", err)
		}

		return
	}

	if targetUser.ID == userID {
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You wind up to throw... at yourself? The snowball decides you need a hug instead.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with self-throw message: %v", err)
		}

		return
	}

	m.stateMu.Lock()
	m.state.ThrowsByUser[userID]++
	m.stateMu.Unlock()

	hitRoll := rand.Float64()
	if hitRoll > 0.75 {
		missTemplates := []string{
			"%s yeets a snowball at %s, but it vaporizes like off-brand pixelated fog. (:poop: no points)",
			"%s lobs a cursed snowball toward %s, only for it to clip through the map and despawn. (:poop: no points)",
			"%s charges up an anime throw at %s, but the snowball whiffs so hard the replay crashes. (:poop: no points)",
			"%s launches a 480p snowball at %s and it rubber-bands back into their own inventory. (:poop: no points)",
			"%s hurls a snowball at %s, but anti-cheat flags it as suspicious aim and deletes it. (:poop: no points)",
			"%s locks onto %s, throws, and the snowball immediately blue-screens reality. (:poop: no points)",
			"%s crafts a snowball for %s so overcompressed it disintegrates into JPEG artifacts mid-air. (:poop: no points)",
			"%s sends a snowball toward %s, but a lag spike teleports it into the Shadow Realm. (:poop: no points)",
			"%s tosses a snowball at %s, but a low-res seagull NPC eats it whole. (:poop: no points)",
			"%s throws their magnum opus snowball at %s and watches it gently alt+F4 out of existence. (:poop: no points)",
		}
		missMsg := missTemplates[rand.IntN(len(missTemplates))]
		message := fmt.Sprintf(missMsg, i.Member.User.Mention(), targetUser.Mention())
		err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: message,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with miss message: %v", err)
		}

		return
	}

	isBigHit := rand.Float64() < 0.10
	points := 1
	var message string
	if isBigHit {
		points = 2
		bigHitTemplates := []string{
			"%s absolutely wallops %s with a snowball so overbuilt it needs patch notes. (:blue_circle: 2 points)",
			"%s channels their inner day-one glitch and hard-crashes %s with a frosty headshot. (:blue_circle: 2 points)",
			"%s unleashes a turbo-charged snowball that explodes over %s like a saturated reaction meme. (:blue_circle: 2 points)",
			"%s lines up the shot, buffer overflows the lobby, and direct-hits %s anyway. (:blue_circle: 2 points)",
			"%s pulls off a wall-bounce trick-shot snowball that ricochets three times before deleting %s from the scene. (:blue_circle: 2 points)",
			"%s crafts an artisanal snowball with 47 shaders and installs it directly onto %s's forehead. (:blue_circle: 2 points)",
			"%s spins up a frosty fastball that hits %s so hard the HUD desyncs. (:blue_circle: 2 points)",
			"%s delivers a slow-motion snowball that ragdolls %s into the upper atmosphere. (:blue_circle: 2 points)",
			"%s summons the legendary RTX 4090 Snowball and overclocks it straight into %s. (:blue_circle: 2 points)",
			"%s lands a crowd-cheering, frame-dropping snowball on %s that the highlight reel will never live down. (:blue_circle: 2 points)",
		}
		bigMsg := bigHitTemplates[rand.IntN(len(bigHitTemplates))]
		message = fmt.Sprintf(bigMsg, i.Member.User.Mention(), targetUser.Mention())
	} else {
		normalHitTemplates := []string{
			"%s lands a scuffed but effective snowbonk on %s. (:white_circle: 1 point)",
			"%s plants a gently cursed snowball right onto %s's avatar. (:white_circle: 1 point)",
			"%s casually bop-installers a snowball update onto %s's face. (:white_circle: 1 point)",
			"%s arcs a crunchy, low-poly snowball through chat and tags %s. (:white_circle: 1 point)",
			"%s sneaks a drive-by snowball past everyone's FOV and taps %s on the shoulder. (:white_circle: 1 point)",
			"%s's snowball hits %s with a deeply unsatisfying but undeniable *thunk*. (:white_circle: 1 point)",
			"%s surprise side-loads a snowball directly into %s's personal space bubble. (:white_circle: 1 point)",
			"%s spin-yeets a mid-quality snowball that still connects with %s. (:white_circle: 1 point)",
			"%s lines up a cozy little snowbonk right on %s's status bar. (:white_circle: 1 point)",
			"%s wings a scuffed snowball across the feed and sticks it to %s. (:white_circle: 1 point)",
		}
		normalMsg := normalHitTemplates[rand.IntN(len(normalHitTemplates))]
		message = fmt.Sprintf(normalMsg, i.Member.User.Mention(), targetUser.Mention())
	}

	m.stateMu.Lock()
	m.state.HitsByUser[userID] += points
	m.state.HitsOnUser[targetUser.ID] += points
	m.stateMu.Unlock()

	err := m.db.AddSnowballScore(userID, i.GuildID, points)
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to add snowball score (%d points for %s): %v", points, userID, err)
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
		},
	})
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to respond with hit message: %v", err)
	}
}

func (m *SnowballModule) handleSnowballScore(s *discordgo.Session, i *discordgo.InteractionCreate) {
	scores, err := m.db.GetTopSnowballScores(i.GuildID, 20)
	if err != nil {
		err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Couldn't fetch the snowball leaderboard. Snowplow hit the database.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with leaderboard fetch error: %v", err)
		}
		return
	}

	if len(scores) == 0 {
		err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "No one has thrown a snowball yet. First hit gets bragging rights!",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with empty leaderboard message: %v", err)
		}
		return
	}

	const maxDiscordMessageLength = 2000
	var leaderboard strings.Builder
	leaderboard.WriteString("❄️ **Snowball Leaderboard** ❄️\n\n")
	for idx, sRow := range scores {
		member, err := s.GuildMember(i.GuildID, sRow.UserID)
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to fetch guild member %s: %v", sRow.UserID, err)
			continue
		}

		name := fmt.Sprintf("User %s", sRow.UserID)
		if member != nil {
			if member.Nick != "" {
				name = member.Nick
			} else {
				name = member.DisplayName()
			}
		}

		line := fmt.Sprintf("%d. %s - %d points\n", idx+1, name, sRow.Score)

		if leaderboard.Len()+len(line) > maxDiscordMessageLength {
			break
		}

		leaderboard.WriteString(line)
	}

	err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: leaderboard.String(),
		},
	})
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to respond with leaderboard: %v", err)
	}
}

func (m *SnowballModule) handleSnowballReset(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Ideally restricted to admins/mods via Discord permissions on the command.
	if err := m.db.ClearSnowballScores(i.GuildID); err != nil {
		err = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Couldn't reset the snowball leaderboard. The database slipped on the ice.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to respond with leaderboard reset error: %v", err)
		}
		return
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "❄️ The snowball leaderboard has been reset for this server.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to respond with leaderboard reset confirmation: %v", err)
	}
}

func (m *SnowballModule) postSummaryAndReset(s *discordgo.Session) {
	// First take a write-lock to check and atomically mark inactive
	// to prevent double-execution from concurrent calls.
	m.stateMu.Lock()
	if !m.state.Active {
		m.stateMu.Unlock()
		return
	}
	// Mark as inactive immediately to prevent re-entry
	m.state.Active = false

	channelID := m.state.ChannelID
	throws := make(map[string]int)
	hits := make(map[string]int)
	hitsOn := make(map[string]int)

	// Deep copy the maps while we have the lock
	for k, v := range m.state.ThrowsByUser {
		throws[k] = v
	}
	for k, v := range m.state.HitsByUser {
		hits[k] = v
	}
	for k, v := range m.state.HitsOnUser {
		hitsOn[k] = v
	}

	// Clear the state maps immediately
	m.state.ChannelID = ""
	m.state.ThrowsByUser = make(map[string]int)
	m.state.HitsByUser = make(map[string]int)
	m.state.HitsOnUser = make(map[string]int)
	m.stateMu.Unlock()

	// Try to remove the snowing emojis from the channel name.
	snowingEmojis := "❄️☃️🌨️"
	channel, err := s.Channel(channelID)
	// Non-fatal if we can't rename the channel.
	if err != nil {
		m.config.Logger.Warnf("snowball: failed to fetch snowfall channel for renaming: %v", err)
	} else if channel != nil {
		newName := strings.TrimSuffix(channel.Name, snowingEmojis)
		_, err = s.ChannelEdit(channelID, &discordgo.ChannelEdit{
			Name: newName,
		})
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to rename snowfall channel: %v", err)
		}
	}

	if len(hits) == 0 {
		_, err := s.ChannelMessageSend(channelID, "The snow gently settles... but nobody threw a single snowball this time.")
		if err != nil {
			m.config.Logger.Warnf("snowball: failed to send no-snowballs message: %v", err)
		}
		// State already cleared above
		return
	}

	type userSummary struct {
		UserID string
		Points int
		Throws int
		HitsOn int
	}

	var summaries []userSummary
	for userID, pts := range hits {
		if pts <= 0 {
			continue
		}
		summaries = append(summaries, userSummary{
			UserID: userID,
			Points: pts,
			Throws: throws[userID],
			HitsOn: hitsOn[userID],
		})
	}

	if len(summaries) == 0 {
		_, _ = s.ChannelMessageSend(channelID, "The snowfall ends quietly. Not a single snowball found its mark.")
		// State already cleared above
		return
	}

	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].Points > summaries[j].Points
	})

	// Find the person who took the most hits and award a pity point.
	var mostHit *userSummary
	for idx := range summaries {
		if mostHit == nil || summaries[idx].HitsOn > mostHit.HitsOn {
			mostHit = &summaries[idx]
		}
	}

	if mostHit != nil && mostHit.HitsOn > 0 {
		guildID := m.config.GetGamerPalsServerID()
		if guildID == "" {
			m.config.Logger.Warn("snowball: pity point could not be recorded because guild ID is empty")
		} else if err := m.db.AddSnowballScore(mostHit.UserID, guildID, 1); err != nil {
			m.config.Logger.Warnf("snowball: failed to add pity point: %v", err)
		}
	}

	const maxDiscordMessageLength = 2000
	content := "❄️ **Snowfall Ended - Score** ❄️\n\n"
	maxShown := 10
	shownCount := 0
	for idx, sRow := range summaries {
		mention := fmt.Sprintf("<@%s>", sRow.UserID)
		line := fmt.Sprintf("%d. %s - %d points from %d throws\n", idx+1, mention, sRow.Points, sRow.Throws)
		if shownCount >= maxShown {
			break
		}
		if len(content)+len(line) > maxDiscordMessageLength {
			break
		}
		content += line
		shownCount++
	}

	if len(summaries) > maxShown {
		remaining := len(summaries) - maxShown
		moreLine := fmt.Sprintf("...and %d more participants.\n", remaining)
		if len(content)+len(moreLine) <= maxDiscordMessageLength {
			content += moreLine
		}
	}

	if mostHit != nil && mostHit.HitsOn > 0 {
		bonusLine := fmt.Sprintf("\nSpecial shoutout to <@%s> for taking the most hits and earning a bonus pity point. You are the true snowbank.", mostHit.UserID)
		if len(content)+len(bonusLine) > maxDiscordMessageLength {
			// If we somehow hit the limit exactly, trim content slightly to make space.
			if len(content) > len(bonusLine) {
				content = content[:maxDiscordMessageLength-len(bonusLine)]
			}
		}
		content += bonusLine
	}

	_, sendErr := s.ChannelMessageSend(channelID, content)
	if sendErr != nil {
		m.config.Logger.Warnf("snowball: failed to send snowfall summary message: %v", sendErr)
	}
}

func (m *SnowballModule) snowfallState() snowfallState {
	m.stateMu.Lock()
	defer m.stateMu.Unlock()

	// Deep copy the state to avoid sharing map references
	s := snowfallState{
		Active:       m.state.Active,
		ChannelID:    m.state.ChannelID,
		EndsAt:       m.state.EndsAt,
		ThrowsByUser: make(map[string]int, len(m.state.ThrowsByUser)),
		HitsByUser:   make(map[string]int, len(m.state.HitsByUser)),
		HitsOnUser:   make(map[string]int, len(m.state.HitsOnUser)),
	}

	for k, v := range m.state.ThrowsByUser {
		s.ThrowsByUser[k] = v
	}
	for k, v := range m.state.HitsByUser {
		s.HitsByUser[k] = v
	}
	for k, v := range m.state.HitsOnUser {
		s.HitsOnUser[k] = v
	}

	return s
}
