package welcome

import (
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"slices"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// WelcomeService handles welcome messages and related functionality
type WelcomeService struct {
	session *discordgo.Session
	config  *config.Config
	nextRun time.Time
	lastRun time.Time
}

// NewWelcomeService creates a new WelcomeService instance
func NewWelcomeService(session *discordgo.Session, config *config.Config) *WelcomeService {
	timeBetweenRuns := config.GetNewPalsTimeBetweenWelcomeMessages()

	return &WelcomeService{
		session: session,
		config:  config,
		nextRun: time.Now().Add(timeBetweenRuns),
		lastRun: time.Now(),
	}
}

// WelcomeNewPals sends a welcome message in the welcome channel to new members
func (ws *WelcomeService) CheckAndWelcomeNewPals() {
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
	members, err := utils.GetAllHumanGuildMembers(ws.session, gamerPalsServerID)
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

	welcomeMessage := heredoc.Docf(`
	%s

	Hi!! Welcome! :green_heart:
	
	I've added you to this channel as a private space for people who are new to the server. Everyone here is also new, so feel free to chat! It's a cozy space just for new pals. If you prefer to jump right into the main chat with the regulars, feel free to do that as well!

	Moderators and other kind folks watch this channel, so feel free to ask any questions. There's no such thing as a dumb question!

	Note: after some time in the server, this channel will go away, keeping it cozy for new pals.
	`,
		newPalsMentionsString,
	)

	// Send the welcome message in the welcome channel
	_, err = ws.session.ChannelMessageSend(welcomeChannelID, welcomeMessage)
	if err != nil {
		ws.config.Logger.Error("Failed to send welcome message:", err)
		ws.config.Logger.Error("I would have sent the message: ", welcomeMessage)
		return
	}

	ws.config.Logger.Infof("Sent welcome message to %d new Pals in channel %s", len(newPals), welcomeChannelID)

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
	messages, err := ws.session.ChannelMessages(welcomeChannelID, 100, "", "", "")
	if err != nil {
		ws.config.Logger.Error("Failed to fetch messages from welcome channel:", err)
		return
	}

	for _, message := range messages[1:] { // Skip the most recent message
		if message.Author.ID == ws.session.State.User.ID {
			err := ws.session.ChannelMessageDelete(welcomeChannelID, message.ID)
			if err != nil {
				ws.config.Logger.Error("Failed to delete old welcome message:", err)
			}
		}
	}
}

// CleanNewPalsRoleFromOldMembers removes the New Pals role from members who have had it for too long
func (ws *WelcomeService) CleanNewPalsRoleFromOldMembers() {
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
	members, err := utils.GetAllHumanGuildMembers(ws.session, guildID)
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
			err := ws.session.GuildMemberRoleRemove(guildID, member.User.ID, newPalsRoleID)
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
