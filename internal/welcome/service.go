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

	ws.config.Logger.Info("Welcome process for new Pals is ready to run")
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
		ws.config.Logger.Info("No new Pals found since last run")
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

	Hi!! Welcome!
	
	I've added you to this channel as a private space for people who are new to the server.

	Everyone here is also new, so feel free to chat! It's a cozy space just for new pals.

	If you prefer to jump right into the main chat, feel free to do that as well!

	Moderators and welcomers monitor this channel, so feel free to ask any questions. There's no such thing as a dumb question!

	Note: after %s in the server, this channel will go away.
	`,
		newPalsMentionsString, ws.config.GetNewPalsKeepRoleDuration().String(),
	)

	// Send the welcome message in the welcome channel
	_, err = ws.session.ChannelMessageSend(welcomeChannelID, welcomeMessage)
	if err != nil {
		ws.config.Logger.Error("Failed to send welcome message:", err)
		ws.config.Logger.Error("I would have sent the message: ", welcomeMessage)
		return
	}

	ws.config.Logger.Infof("Sent welcome message to %d new Pals in channel %s", len(newPals), welcomeChannelID)
}

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
		roleAddedAt := member.JoinedAt.Add(newPalsKeepRoleDuration)
		if time.Now().After(roleAddedAt) {
			err := ws.session.GuildMemberRoleRemove(guildID, member.User.ID, newPalsRoleID)
			if err != nil {
				ws.config.Logger.Error("Failed to remove New Pals role from member %s (%s): %v",
					member.User.Username, member.User.ID, err)
				continue
			}

			ws.config.Logger.Infof("Removed New Pals role from member %s (%s) after %s",
				member.User.Username, member.User.ID, newPalsKeepRoleDuration)
		}
	}

	ws.config.Logger.Info("Finished cleaning up New Pals roles")
}
