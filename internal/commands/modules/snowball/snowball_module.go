package snowball

import (
	"fmt"
	"math/rand/v2"
	"sort"
	"time"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/bwmarrin/discordgo"
)

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

	state snowfallState
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

	cmds["snowfall"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "snowfall",
			Description: "Start or stop a festive snowfall",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
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

	cmds["snowball-score"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "snowball-score",
			Description: "View the snowball leaderboard",
			Contexts:    &[]discordgo.InteractionContextType{discordgo.InteractionContextGuild},
		},
		HandlerFunc: m.handleSnowballScore,
	}

	_ = adminPerms
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
	if m.state.Active {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "It's already snowing somewhere! Use /snowfall stop first.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
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
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Please provide a valid channel and duration.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	m.state = snowfallState{
		Active:       true,
		ChannelID:    channelID,
		EndsAt:       time.Now().Add(time.Duration(minutes) * time.Minute),
		ThrowsByUser: make(map[string]int),
		HitsByUser:   make(map[string]int),
		HitsOnUser:   make(map[string]int),
	}

	gifURL := "https://www.animationsoftware7.com/img/agifs/snow02.gif"

	_, _ = s.ChannelMessageSend(m.state.ChannelID, fmt.Sprintf("❄️ It's snowing! Use /snowball to join the snowball fight!\n%s", gifURL))

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Started a snowfall in <#%s> for %d minutes.", channelID, minutes),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})

	go m.autoStopAfterDuration(s)
}

func (m *SnowballModule) handleSnowfallStop(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption) {
	if !m.state.Active {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "There isn't an active snowfall right now.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	var channelID string
	for _, opt := range sub.Options {
		if opt.Name == "channel" && opt.ChannelValue(s) != nil {
			channelID = opt.ChannelValue(s).ID
		}
	}

	if channelID == "" || channelID != m.state.ChannelID {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "That channel doesn't match the active snowfall.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	m.postSummaryAndReset(s)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: "Snowfall stopped.",
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}

func (m *SnowballModule) autoStopAfterDuration(s *discordgo.Session) {
	for {
		if !m.state.Active {
			return
		}
		if time.Now().After(m.state.EndsAt) {
			m.postSummaryAndReset(s)
			return
		}
		time.Sleep(5 * time.Second)
	}
}

func (m *SnowballModule) handleSnowball(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if !m.state.Active {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "It isn't snowing right now, so your snowball just melts in your hands.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if i.Member == nil || i.Member.User == nil {
		m.config.Logger.Warn("snowball: interaction missing member or user; ignoring snowball command")
		return
	}

	userID := i.Member.User.ID
	if m.state.ThrowsByUser[userID] >= 3 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You've already thrown 3 snowballs this snowfall. Save some snow for everyone else!",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	cmdData := i.ApplicationCommandData()
	if len(cmdData.Options) == 0 || len(cmdData.Options[0].Options) == 0 {
		m.config.Logger.Warn("snowball: missing target option on command; ignoring")
		return
	}

	targetOpt := cmdData.Options[0].Options[0]
	targetUser := targetOpt.UserValue(s)
	if targetUser == nil {
		return
	}
	if targetUser.ID == userID {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "You wind up to throw... at yourself? The snowball decides you need a hug instead.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	m.state.ThrowsByUser[userID]++

	hitRoll := rand.Float64()
	if hitRoll > 0.75 {
		missTemplates := []string{
			"%s lets a snowball fly at %s, but it poofs into powder mid-air.",
			"%s lobs a snowball toward %s, but it veers off and joins a nearby snowdrift.",
			"%s winds up and throws at %s, but the snowball softly bonks into a passing snowman instead.",
			"%s launches a snowball at %s, but it shatters against an invisible force field of coziness.",
			"%s hurls a snowball at %s, but it crumbles in their hands like a dramatic telenovela scene.",
			"%s aims at %s, but the snowball takes one look at the chaos and simply decides ‘nope’.",
			"%s scoops up a snowball for %s, but trips, giggles, and ends up dusted in snow instead.",
			"%s sends a snowball toward %s, but a gust of wind yoinks it into the winter night.",
			"%s tosses a snowball at %s, but a friendly penguin intercepts it and waddles off.",
			"%s throws their best snowball at %s, but it dissolves into sparkling flakes before it lands.",
		}
		missMsg := missTemplates[rand.IntN(len(missTemplates))]
		message := fmt.Sprintf(missMsg, i.Member.User.Mention(), targetUser.Mention())
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: message,
			},
		})
		return
	}

	isBigHit := rand.Float64() < 0.10
	points := 1
	var message string
	if isBigHit {
		points = 2
		bigHitTemplates := []string{
			"%s absolutely wallops %s with a perfectly packed snowball for **2 points**!",
			"%s channels their inner blizzard and smacks %s with a heavy-duty snowball for **2 points**!",
			"%s unleashes a turbo-charged snowball that detonates in fluffy glory all over %s — **2 points**!",
			"%s lines up the shot, and *bam* — direct hit on %s for **2 points**!",
			"%s pulls off a trick-shot snowball that ricochets off a lamppost and nails %s for **2 points**!",
			"%s crafts an artisanal, perfectly symmetrical snowball and domes %s with it for **2 points**!",
			"%s spins up a frosty fastball and plants it right in %s's snowbank for **2 points**!",
			"%s delivers a cinematic slow-motion snowball that absolutely splashes across %s — **2 points**!",
			"%s summons the legendary Snowball of Destiny and bonks %s for a glorious **2 points**!",
			"%s lands a crowd-cheering, clip-worthy snowball on %s for **2 points**!",
		}
		bigMsg := bigHitTemplates[rand.IntN(len(bigHitTemplates))]
		message = fmt.Sprintf(bigMsg, i.Member.User.Mention(), targetUser.Mention())
	} else {
		normalHitTemplates := []string{
			"%s lands a snowy hit on %s!",
			"%s pegs %s with a perfectly chilled snowball.",
			"%s gently but decisively bops %s with a puff of snow.",
			"%s arcs a snowball through the winter air and tags %s.",
			"%s sneaks a snowball past everyone's guard and taps %s right on the shoulder.",
			"%s's snowball thuds into %s with a satisfying *fwump*.",
			"%s surprises %s with a sneaky side-angle snowball.",
			"%s spins, yeets, and successfully plants a snowball on %s.",
			"%s lines up a comfy little snowbonk right on %s.",
			"%s wings a snowball across the chat and lands it squarely on %s.",
		}
		normalMsg := normalHitTemplates[rand.IntN(len(normalHitTemplates))]
		message = fmt.Sprintf(normalMsg, i.Member.User.Mention(), targetUser.Mention())
	}

	m.state.HitsByUser[userID] += points
	m.state.HitsOnUser[targetUser.ID] += points

	_ = m.db.AddSnowballScore(userID, i.GuildID, points)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: message,
		},
	})
}

func (m *SnowballModule) handleSnowballScore(s *discordgo.Session, i *discordgo.InteractionCreate) {
	scores, err := m.db.GetTopSnowballScores(i.GuildID, 20)
	if err != nil {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "Couldn't fetch the snowball leaderboard. The snowplow hit the database.",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	if len(scores) == 0 {
		_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: "No one has thrown a snowball yet. First hit gets bragging rights!",
				Flags:   discordgo.MessageFlagsEphemeral,
			},
		})
		return
	}

	content := "❄️ **Snowball Leaderboard** ❄️\n\n"
	for idx, sRow := range scores {
		member, _ := s.GuildMember(i.GuildID, sRow.UserID)
		name := fmt.Sprintf("<@%s>", sRow.UserID)
		if member != nil && member.Nick != "" {
			name = member.Mention()
		}
		content += fmt.Sprintf("%d. %s — %d points\n", idx+1, name, sRow.Score)
	}

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
		},
	})
}

func (m *SnowballModule) postSummaryAndReset(s *discordgo.Session) {
	if !m.state.Active {
		return
	}

	channelID := m.state.ChannelID
	throws := m.state.ThrowsByUser
	hits := m.state.HitsByUser
	hitsOn := m.state.HitsOnUser

	if len(hits) == 0 {
		_, _ = s.ChannelMessageSend(channelID, "The snow gently settles... but nobody threw a single snowball this time.")
		m.state.Active = false
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
		m.state.Active = false
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

	content := "❄️ **Snowfall Summary** ❄️\n\n"
	for idx, sRow := range summaries {
		mention := fmt.Sprintf("<@%s>", sRow.UserID)
		line := fmt.Sprintf("%d. %s — %d points from %d throws\n", idx+1, mention, sRow.Points, sRow.Throws)
		content += line
	}

	if mostHit != nil && mostHit.HitsOn > 0 {
		content += fmt.Sprintf("\nSpecial shoutout to <@%s> for taking the most hits and earning a bonus pity point. You are the true snowbank.", mostHit.UserID)
	}

	_, _ = s.ChannelMessageSend(channelID, content)

	m.state.Active = false
	m.state.ChannelID = ""
	m.state.ThrowsByUser = make(map[string]int)
	m.state.HitsByUser = make(map[string]int)
	m.state.HitsOnUser = make(map[string]int)
}
