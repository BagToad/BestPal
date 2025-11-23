package welcome

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"
	"slices"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

const defaultMsg = `Hi!! Welcome! 💚

I've added you to this channel as a _private space_ for people who are new to the server. Everyone here is also new, so feel free to chat! This is a cozy channel just for new pals.

If you prefer to jump right into the main chat with the regulars, please do!

Moderators and other kind folks are available if you need them, so please ask any questions. There's no such thing as a dumb question!

Here are a few key areas of the server you might be looking for as a new member:
<#1375605443933507694> - Post an introduction here to let others find you!
<#1414445853816524830> - Right click & follow *and/or* comment on the games you like to keep up with LFG posts for your favs!
<#1414752418758918144> - Check out our current events & niche clubs where you can meet new friends with shared interests`

// WelcomeService handles welcome messages and related functionality
type WelcomeService struct {
	types.BaseService
	config  *config.Config
	nextRun time.Time
	lastRun time.Time
	db      *database.DB
}

// NewWelcomeService creates a new WelcomeService instance
func NewWelcomeService(deps *types.Dependencies) *WelcomeService {
	config := deps.Config
	db := deps.DB

	timeBetweenRuns := config.GetNewPalsTimeBetweenWelcomeMessages()

	return &WelcomeService{
		config:  config,
		nextRun: time.Now().Add(timeBetweenRuns),
		lastRun: time.Now(),
		db:      db,
	}
}

// WelcomeNewPals sends a welcome message in the welcome channel to new members
func (ws *WelcomeService) CheckAndWelcomeNewPals() {
	if ws.Session == nil {
		ws.config.Logger.Warn("Discord session not initialized, skipping welcome process")
		return
	}

	gamerPalsServerID := ws.config.GetGamerPalsServerID()
	timeBetweenRuns := ws.config.GetNewPalsTimeBetweenWelcomeMessages()
	isNewPalsEnabled := ws.config.GetNewPalsSystemEnabled()
	welcomeChannelID := ws.config.GetNewPalsChannelID()
	newPalsRoleID := ws.config.GetNewPalsRoleID()

	if !isNewPalsEnabled {
		ws.config.Logger.Info("New Pals system is disabled, skipping welcome process")
		return
	}

	requiredConfigsSet := welcomeChannelID != "" && newPalsRoleID != "" && timeBetweenRuns > 0
	if !requiredConfigsSet {
		ws.config.Logger.Error("Required configurations for New Pals system are not set, skipping welcome process")
		return
	}

	if time.Now().Before(ws.nextRun) {
		ws.config.Logger.Info("Welcome process is not ready to run yet")
		return
	}

	defer func() {
		ws.lastRun = time.Now()
	}()

	// Fetch the list of members in the guild
	members, err := utils.GetAllHumanGuildMembers(ws.Session, gamerPalsServerID)
	if err != nil {
		ws.config.Logger.Error("Failed to fetch guild members: %v", err)
		return
	}

	var newPals []*discordgo.Member
	for _, member := range members {
		if member.JoinedAt.After(ws.lastRun) && slices.Contains(member.Roles, newPalsRoleID) {
			newPals = append(newPals, member)
		}
	}

	if len(newPals) == 0 {
		ws.config.Logger.Debug("No new Pals found since last run")
		return
	}

	ws.config.Logger.Infof("Found %d new Pals to welcome", len(newPals))

	// Build the welcome message
	var newPalsMentions []string
	for _, member := range newPals {
		newPalsMentions = append(newPalsMentions, member.Mention())
	}
	newPalsMentionsString := strings.Join(newPalsMentions, " ")

	welcomeMsg, err := ws.db.GetWelcomeMessage()
	if err != nil {
		welcomeMsg = defaultMsg // Since we couldn't get any message from the DB we default to the old message
	}

	welcomeMsg = heredoc.Docf(`
		%s

		%s
	`, newPalsMentionsString, welcomeMsg)

	// Send the welcome message in the welcome channel
	_, err = ws.Session.ChannelMessageSend(welcomeChannelID, welcomeMsg)
	if err != nil {
		ws.config.Logger.Error("Failed to send welcome message:", err)
		ws.config.Logger.Error("I would have sent the message: ", welcomeMsg)
		return
	}

	ws.config.Logger.Infof("Sent welcome message to %d new Pals in channel %s", len(newPals), welcomeChannelID)
	ws.nextRun = time.Now().Add(timeBetweenRuns)

	ws.cleanOldWelcomeMessages()
}

// cleanOldWelcomeMessages cleans up old welcome messages in the welcome channel
func (ws *WelcomeService) cleanOldWelcomeMessages() {
	welcomeChannelID := ws.config.GetNewPalsChannelID()

	if welcomeChannelID == "" {
		ws.config.Logger.Error("No welcome channel ID configured, skipping cleanup")
		return
	}

	ws.config.Logger.Infof("Cleaning up old welcome messages in channel: %s", welcomeChannelID)

	// Fetch the messages in the welcome channel
	messages, err := ws.Session.ChannelMessages(welcomeChannelID, 100, "", "", "")
	if err != nil {
		ws.config.Logger.Error("Failed to fetch messages from welcome channel:", err)
		return
	}

	for _, message := range messages[1:] { // Skip the most recent message
		if message.Author.ID == ws.Session.State.User.ID {
			err := ws.Session.ChannelMessageDelete(welcomeChannelID, message.ID)
			if err != nil {
				ws.config.Logger.Error("Failed to delete old welcome message:", err)
			}
		}
	}
}

// CleanNewPalsRoleFromOldMembers removes the New Pals role from members who have had it for too long
func (ws *WelcomeService) CleanNewPalsRoleFromOldMembers() {
	if ws.Session == nil {
		ws.config.Logger.Warn("Discord session not initialized, skipping New Pals cleanup")
		return
	}

	guildID := ws.config.GetGamerPalsServerID()
	newPalsRoleID := ws.config.GetNewPalsRoleID()
	newPalsKeepRoleDuration := ws.config.GetNewPalsKeepRoleDuration()
	isNewPalsEnabled := ws.config.GetNewPalsSystemEnabled()

	if !isNewPalsEnabled {
		ws.config.Logger.Info("New Pals system is disabled, skipping cleanup")
		return
	}

	if newPalsRoleID == "" {
		ws.config.Logger.Error("No New Pals role ID configured, skipping cleanup")
		return
	}

	if newPalsKeepRoleDuration <= 0 {
		ws.config.Logger.Error("New Pals keep role duration is not set or invalid, skipping cleanup")
		return
	}

	ws.config.Logger.Infof("Cleaning up New Pals role from members older than %s", newPalsKeepRoleDuration.String())

	// Fetch all members in the guild
	members, err := utils.GetAllHumanGuildMembers(ws.Session, guildID)
	if err != nil {
		ws.config.Logger.Error("Failed to fetch guild members:", err)
		return
	}

	for _, member := range members {
		if !slices.Contains(member.Roles, newPalsRoleID) {
			continue // Skip if the member doesn't have the New Pals role
		}

		// Check how long the member has had the role
		roleExpirationTime := member.JoinedAt.Add(newPalsKeepRoleDuration)
		if time.Now().After(roleExpirationTime) {
			err := ws.Session.GuildMemberRoleRemove(guildID, member.User.ID, newPalsRoleID)
			if err != nil {
				ws.config.Logger.Error("Failed to remove New Pals role from member %s (%s): %v",
					member.User.Username, member.User.ID, err)
				continue
			}

			ws.config.Logger.Infof("Removed New Pals role from member %s (%s) after %s",
				member.User.Username, member.User.ID, time.Since(member.JoinedAt))
		}
	}

	ws.config.Logger.Info("Finished cleaning up New Pals roles")
}

// MinuteFuncs returns functions to be called every minute
func (ws *WelcomeService) MinuteFuncs() []func() error {
	return []func() error{
		func() error {
			ws.CleanNewPalsRoleFromOldMembers()
			ws.CheckAndWelcomeNewPals()
			return nil
		},
	}
}

// HourFuncs returns nil as this service has no hourly tasks
func (ws *WelcomeService) HourFuncs() []func() error {
	return nil
}
