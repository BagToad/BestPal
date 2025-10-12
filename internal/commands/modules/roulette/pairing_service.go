package roulette

import (
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"
	"math/rand"
	"slices"
	"strings"
	"time"

	"github.com/MakeNowJust/heredoc"
	"github.com/bwmarrin/discordgo"
)

// User represents a user in the pairing system
type User struct {
	UserID  string
	Games   []string
	Regions []string
	Paired  bool
}

// PairingService handles pairing-related operations
type PairingService struct {
	session *discordgo.Session
	config  *config.Config
	db      *database.DB
}

// NewPairingService creates a new pairing service
func NewPairingService(session *discordgo.Session, cfg *config.Config, db *database.DB) *PairingService {
	return &PairingService{
		session: session,
		config:  cfg,
		db:      db,
	}
}

// RegionRoles returns the mapping of region names to Discord role IDs
func (s *PairingService) getRegionRoles() map[string]string {
	return map[string]string{
		"NA": "475040060786343937",
		"EU": "475039994554351618",
		"SA": "475040095993593866",
		"AS": "475040122463846422",
		"OC": "505413573586059266",
		"ZA": "518493780308000779",
	}
}

// BuildUserPairingData builds user data for pairing from the database
func (s *PairingService) BuildUserPairingData(guildID string) ([]User, error) {
	signups, err := s.db.GetRouletteSignups(guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to get roulette signups: %w", err)
	}

	regions := s.getRegionRoles()
	var users []User

	for _, signup := range signups {
		// Get user's games
		games, err := s.db.GetRouletteGames(signup.UserID, guildID)
		if err != nil {
			s.config.Logger.Warn("Error getting games for user %s: %v", signup.UserID, err)
			continue
		}

		gameNames := make([]string, len(games))
		for i, game := range games {
			gameNames[i] = game.GameName
		}

		// Get user's regions
		member, err := s.session.GuildMember(guildID, signup.UserID)
		if err != nil {
			s.config.Logger.Errorf("Error getting guild member %s: %v", signup.UserID, err)
			continue
		}

		var userRegions []string
		for region, roleID := range regions {
			for _, role := range member.Roles {
				if role == roleID {
					userRegions = append(userRegions, region)
					break
				}
			}
		}

		users = append(users, User{
			UserID:  signup.UserID,
			Games:   gameNames,
			Regions: userRegions,
			Paired:  false,
		})
	}

	return users, nil
}

// FindOptimalPairings implements the randomized pairing algorithm for groups of 4
// This algorithm prioritizes variety over optimal matches to ensure different groups each time:
// 1. Randomizes user list for variety
// 2. For each group: tries to find 4 users with matching games + regions
// 3. If not possible: tries to find 4 users with matching games only
// 4. If still not possible: skips and tries with remaining users
func (s *PairingService) FindOptimalPairings(users []User) [][]User {
	var groups [][]User

	// Make a copy to work with
	availableUsers := make([]User, len(users))
	copy(availableUsers, users)

	// Shuffle for randomness
	for i := range availableUsers {
		j := i + rand.Intn(len(availableUsers)-i)
		availableUsers[i], availableUsers[j] = availableUsers[j], availableUsers[i]
	}

	// Form groups of 4
	for len(availableUsers) >= 4 {
		bestGroup := s.formGroup(availableUsers)
		if bestGroup != nil {
			groups = append(groups, bestGroup)

			// Mark as paired in original slice
			for i := range users {
				for _, groupUser := range bestGroup {
					if users[i].UserID == groupUser.UserID {
						users[i].Paired = true
						break
					}
				}
			}

			// Remove grouped users from available users
			newAvailable := []User{}
			for _, user := range availableUsers {
				isInGroup := false
				for _, groupUser := range bestGroup {
					if user.UserID == groupUser.UserID {
						isInGroup = true
						break
					}
				}
				if !isInGroup {
					newAvailable = append(newAvailable, user)
				}
			}
			availableUsers = newAvailable
		} else {
			// No suitable group found, remove first user and try again
			availableUsers = availableUsers[1:]
		}
	}

	return groups
}

// formGroup finds the first group of 4 users with like games and regions
func (s *PairingService) formGroup(users []User) []User {
	if len(users) < 4 {
		return nil
	}

	// Collect minimally viable candidates.
	// Start with the first user as anchor.
	anchor := users[0]
	var gameAndRegionCandidates []User
	var gameOnlyCandidates []User

	// Find candidates that match criteria with the anchor.
	for _, candidate := range users[1:] {
		commonGames := s.FindCommonGames(anchor.Games, candidate.Games)
		gamesMatch := len(commonGames) > 0

		regionsMatch := s.hasCommonRegion(anchor.Regions, candidate.Regions)

		if gamesMatch && regionsMatch {
			gameAndRegionCandidates = append(gameAndRegionCandidates, candidate)
			gameOnlyCandidates = append(gameOnlyCandidates, candidate)
		}

		if gamesMatch && !regionsMatch {
			gameOnlyCandidates = append(gameOnlyCandidates, candidate)
		}
	}

	allCandidates := append(gameAndRegionCandidates, gameOnlyCandidates...)

	// Need at least 3 more users to form a group of 4.
	if len(allCandidates) < 3 {
		return nil
	}

	// Now that we have individual candidates, we need to form a group of 4.
	// Challenge: everyone in a group must have at least one common game.
	var group []User

	// First, try to form a group with both matching games and regions.
	group = s.formGroupFromCandidates(anchor, gameAndRegionCandidates)
	if len(group) == 4 {
		return group
	}

	// If we didn't find enough candidates with matching games and regions,
	// try to form a group with matching games only.
	group = s.formGroupFromCandidates(anchor, gameOnlyCandidates)
	if len(group) == 4 {
		return group
	}

	return nil
}

// formGroupFromCandidates tries to form a group of 4 users from the candidates
func (s *PairingService) formGroupFromCandidates(anchor User, candidates []User) []User {
	// We'll start by making a prospective group using the anchor and the first candidate.
	if len(candidates) >= 3 {
		group := []User{anchor, candidates[0]}
		// The first candidate's matching games will be considered the common games for the group, and
		// candidates missing those games will be ignored.
		groupCommonGames := s.FindCommonGames(anchor.Games, candidates[0].Games)

		for _, c := range candidates[1:] {
			// Check if candidate has any common games with the group
			commonGamesWithGroupCandidate := s.FindCommonGames(anchor.Games, c.Games)

			// Matches none of the common games for the group.
			if !slices.ContainsFunc(commonGamesWithGroupCandidate, func(candidateGame string) bool {
				return slices.Contains(groupCommonGames, candidateGame)
			}) {
				continue
			}

			// It's a match, add to group
			group = append(group, c)

			// If we have 4 users, return the final group
			if len(group) == 4 {
				return group
			}
		}
	}

	return nil
}

// FindCommonGames finds games that are in both slices
func (s *PairingService) FindCommonGames(games1, games2 []string) []string {
	var common []string

	for _, game := range games1 {
		if slices.Contains(games2, game) {
			common = append(common, game)
		}
	}

	slices.Sort(common)
	return common
}

// hasCommonRegion checks if two region slices have any common regions
func (s *PairingService) hasCommonRegion(regions1, regions2 []string) bool {
	return slices.ContainsFunc(regions1, func(region1 string) bool {
		return slices.Contains(regions2, region1)
	})
}

// CleanupPreviousPairings deletes previous pairing channels
func (s *PairingService) CleanupPreviousPairings(guildID string) error {
	pairingCategoryID := s.config.GetGamerPalsPairingCategoryID()
	if pairingCategoryID == "" {
		return fmt.Errorf("pairing category ID not configured")
	}

	channels, err := s.session.GuildChannels(guildID)
	if err != nil {
		return fmt.Errorf("failed to get guild channels: %w", err)
	}

	for _, channel := range channels {
		if channel.ParentID == pairingCategoryID {
			_, err := s.session.ChannelDelete(channel.ID)
			if err != nil {
				s.config.Logger.Errorf("Error deleting channel %s: %v", channel.Name, err)
			}
		}
	}

	return nil
}

// CreatePairingChannels creates text and voice channels for a pair
func (s *PairingService) CreatePairingChannels(guildID string, pair []User, commonGames []string) error {
	pairingCategoryID := s.config.GetGamerPalsPairingCategoryID()
	if pairingCategoryID == "" {
		return fmt.Errorf("pairing category ID not configured")
	}

	nouns := utils.Nouns
	adjectives := utils.Adjectives

	// Create unique random channel name of adjective-noun format
	words := []string{
		adjectives[rand.Intn(len(adjectives))],
		nouns[rand.Intn(len(nouns))],
	}

	channelName := fmt.Sprintf("group-%s", strings.Join(words, "-"))

	// Truncate channel name if necessary
	if len(channelName) > 100 {
		channelName = channelName[:97] + "..."
	}

	if c, err := s.session.Channel(channelName); err == nil && c != nil {
		// probably already exists, add a number suffix
		channelExists := true
		suffix := 1
		for channelExists {
			newName := fmt.Sprintf("%s-%d", channelName, suffix)
			if _, err := s.session.Channel(newName); err != nil {
				channelName = newName
				channelExists = false
			} else {
				suffix++
			}
		}

	}

	// Set up permissions
	permissions := []*discordgo.PermissionOverwrite{
		{
			ID:   guildID,
			Type: discordgo.PermissionOverwriteTypeRole,
			Deny: discordgo.PermissionViewChannel,
		},
	}

	for _, user := range pair {
		// if not a guild member, don't update with these (testing)
		m, err := s.session.GuildMember(guildID, user.UserID)
		if err != nil || m == nil {
			continue
		}

		permissions = append(permissions, &discordgo.PermissionOverwrite{
			ID:    user.UserID,
			Type:  discordgo.PermissionOverwriteTypeMember,
			Allow: discordgo.PermissionViewChannel | discordgo.PermissionSendMessages | discordgo.PermissionVoiceConnect | discordgo.PermissionVoiceSpeak | discordgo.PermissionReadMessageHistory,
		})
	}

	// Create text channel
	textChannel, err := s.session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 channelName,
		Type:                 discordgo.ChannelTypeGuildText,
		ParentID:             pairingCategoryID,
		PermissionOverwrites: permissions,
	})

	if err != nil {
		return fmt.Errorf("failed to create text channel: %w", err)
	}

	// Send welcome message
	var userMentions []string
	for _, user := range pair {
		userMentions = append(userMentions, fmt.Sprintf("<@%s>", user.UserID))
	}

	message := heredoc.Docf(`
	🎰 **Your roulette pairing is ready!**
	
	Hi %s! This is your pairing channel; you can use it to coordinate a time and chat!

	We also made you a private voice channel for you to use, if you'd like.

	Remember, this pairing is open for the whole week - if someone doesn't reply right away, they might be at work :) Try to find a time that works for everyone and be patient!

	Do your best and have fun!
	`, strings.Join(userMentions, " "))

	if len(commonGames) > 0 {
		message += fmt.Sprintf("You all like: %s", strings.Join(commonGames, ", "))
	}

	_, err = s.session.ChannelMessageSend(textChannel.ID, message)
	if err != nil {
		s.config.Logger.Errorf("Error sending welcome message: %v", err)
	}

	// Create voice channel
	_, err = s.session.GuildChannelCreateComplex(guildID, discordgo.GuildChannelCreateData{
		Name:                 channelName + "-voice",
		Type:                 discordgo.ChannelTypeGuildVoice,
		ParentID:             pairingCategoryID,
		PermissionOverwrites: permissions,
	})

	if err != nil {
		s.config.Logger.Errorf("Error creating voice channel: %v", err)
	}

	return nil
}

// ExecutePairing executes the complete pairing process
func (s *PairingService) ExecutePairing(guildID string, dryRun bool) (*PairingResult, error) {
	// Build user data
	users, err := s.BuildUserPairingData(guildID)
	if err != nil {
		return nil, fmt.Errorf("failed to build user data: %w", err)
	}

	if len(users) < 4 {
		return &PairingResult{
			Success:      false,
			ErrorMessage: fmt.Sprintf("Need at least 4 users signed up for pairing (got %d)", len(users)),
			TotalUsers:   len(users),
		}, nil
	}

	// Execute pairing algorithm
	groups := s.FindOptimalPairings(users)

	result := &PairingResult{
		Success:    len(groups) > 0,
		Pairs:      groups,
		TotalUsers: len(users),
		PairCount:  len(groups),
	}

	if len(groups) == 0 {
		result.ErrorMessage = "No suitable groups found"
		return result, nil
	}

	// Find unpaired users
	for _, user := range users {
		if !user.Paired {
			result.UnpairedUsers = append(result.UnpairedUsers, user.UserID)
		}
	}

	if !dryRun {
		// Clean up previous pairing channels
		if err := s.CleanupPreviousPairings(guildID); err != nil {
			s.config.Logger.Errorf("Error cleaning up previous pairings: %v", err)
		}

		// Create pairing channels
		for _, group := range groups {
			commonGames := []string{}
			if len(group) >= 2 {
				commonGames = group[0].Games
				// For groups, find games common to all members
				for j := 1; j < len(group); j++ {
					commonGames = s.FindCommonGames(commonGames, group[j].Games)
				}
			}

			if err := s.CreatePairingChannels(guildID, group, commonGames); err != nil {
				s.config.Logger.Errorf("Error creating pairing channels: %v", err)
			}
		}

		// Clear the schedule if this was a scheduled pairing
		if err := s.db.ClearRouletteSchedule(guildID); err != nil {
			s.config.Logger.Errorf("Error clearing schedule: %v", err)
		}
	}

	return result, nil
}

// PairingResult represents the result of a pairing operation
type PairingResult struct {
	Success       bool
	ErrorMessage  string
	Pairs         [][]User
	UnpairedUsers []string
	TotalUsers    int
	PairCount     int
}

// SimulatePairing generates simulated pairing data for testing purposes
func (s *PairingService) SimulatePairing(guildID string, userCount int, createChannels bool) (*PairingResult, error) {
	if userCount < 4 {
		// For user counts less than 4, return all users as unpaired
		fakeUsers := s.generateFakeUsers(userCount)
		unpairedUserIDs := make([]string, len(fakeUsers))
		for i, user := range fakeUsers {
			unpairedUserIDs[i] = user.UserID
		}

		return &PairingResult{
			Success:       false,
			ErrorMessage:  fmt.Sprintf("Need at least 4 users for simulation (got %d)", userCount),
			TotalUsers:    userCount,
			PairCount:     0,
			Pairs:         [][]User{},
			UnpairedUsers: unpairedUserIDs,
		}, nil
	}

	// Generate fake users with random games and regions
	fakeUsers := s.generateFakeUsers(userCount)

	groups := s.FindOptimalPairings(fakeUsers)

	result := &PairingResult{
		Success:    len(groups) > 0,
		Pairs:      groups,
		TotalUsers: len(fakeUsers),
		PairCount:  len(groups),
	}

	if len(groups) == 0 {
		result.ErrorMessage = "No suitable groups found in simulation"
		// All users are unpaired if no groups formed
		for _, user := range fakeUsers {
			result.UnpairedUsers = append(result.UnpairedUsers, user.UserID)
		}
		return result, nil
	}

	// Find unpaired users
	for _, user := range fakeUsers {
		if !user.Paired {
			result.UnpairedUsers = append(result.UnpairedUsers, user.UserID)
		}
	}

	if createChannels {
		// Clean up previous pairing channels
		if err := s.CleanupPreviousPairings(guildID); err != nil {
			s.config.Logger.Errorf("Error cleaning up previous pairings: %v", err)
		}

		// Create pairing channels with fake users
		for _, group := range groups {
			commonGames := []string{}
			if len(group) >= 2 {
				commonGames = group[0].Games
				// For groups, find games common to all members
				for j := 1; j < len(group); j++ {
					commonGames = s.FindCommonGames(commonGames, group[j].Games)
				}
			}

			if err := s.CreatePairingChannels(guildID, group, commonGames); err != nil {
				s.config.Logger.Errorf("Error creating simulation pairing channels: %v", err)
			}
		}
	}

	return result, nil
}

// generateFakeUsers creates fake user data for simulation purposes
func (s *PairingService) generateFakeUsers(count int) []User {
	var users []User

	// Use a smaller set of very popular games to maximize overlap
	popularGames := []string{
		"Valorant", "Counter-Strike 2", "Overwatch 2", "League of Legends", "Rocket League",
	}

	for i := 0; i < count; i++ {
		// Generate fake user ID
		userID := fmt.Sprintf("fake_user_%d", i+1)

		var userGames []string
		var userRegions []string

		// Create deterministic but varied patterns to ensure some overlap
		// This ensures at least some groups can be formed for testing
		gamePattern := i % len(popularGames)
		userGames = append(userGames, popularGames[gamePattern])

		// Add a second game with some overlap pattern
		if i%3 == 0 { // Every 3rd user gets a second game
			secondGamePattern := (i + 1) % len(popularGames)
			if secondGamePattern != gamePattern {
				userGames = append(userGames, popularGames[secondGamePattern])
			}
		}

		// Region assignment with bias towards NA/EU and some overlap
		regionPattern := i % 4
		if regionPattern < 2 {
			userRegions = []string{"NA"}
		} else {
			userRegions = []string{"EU"}
		}

		// Add some users with both regions for flexibility
		if i%5 == 0 {
			userRegions = []string{"NA", "EU"}
		}

		users = append(users, User{
			UserID:  userID,
			Games:   userGames,
			Regions: userRegions,
			Paired:  false,
		})
	}

	return users
}

// LogPairingResults logs the pairing results to the mod action log
func (s *PairingService) LogPairingResults(guildID string, result *PairingResult, isScheduled bool) {
	modLogChannelID := s.config.GetGamerPalsModActionLogChannelID()
	if modLogChannelID == "" {
		return
	}

	var message string
	if isScheduled {
		if result.Success {
			message = fmt.Sprintf("✅ **Automated Roulette Pairing Executed**\n\n"+
				"**Guild:** %s\n"+
				"**Groups Created:** %d\n"+
				"**Unpaired Users:** %d\n"+
				"**Time:** <t:%d:F>",
				guildID, result.PairCount, len(result.UnpairedUsers), time.Now().Unix())
		} else {
			message = fmt.Sprintf("⚠️ **Automated Roulette Pairing Failed**\n\n"+
				"**Guild:** %s\n"+
				"**Reason:** %s\n"+
				"**Time:** <t:%d:F>\n\n"+
				"Please check the roulette system and manually trigger pairing if needed.",
				guildID, result.ErrorMessage, time.Now().Unix())
		}
	} else {
		// Manual pairing - send detailed report as file
		var report strings.Builder
		report.WriteString("ROULETTE PAIRING AUDIT REPORT\n")
		report.WriteString(fmt.Sprintf("Timestamp: %s\n", time.Now().Format("2006-01-02 15:04:05 UTC")))
		report.WriteString(fmt.Sprintf("Guild ID: %s\n\n", guildID))

		report.WriteString(fmt.Sprintf("GROUPS CREATED: %d\n", result.PairCount))
		for i, group := range result.Pairs {
			report.WriteString(fmt.Sprintf("\nGroup %d:\n", i+1))
			for _, user := range group {
				report.WriteString(fmt.Sprintf("- User ID: %s\n", user.UserID))
			}
		}

		if len(result.UnpairedUsers) > 0 {
			report.WriteString(fmt.Sprintf("\nUNPAIRED USERS: %d\n", len(result.UnpairedUsers)))
			for _, userID := range result.UnpairedUsers {
				report.WriteString(fmt.Sprintf("- %s\n", userID))
			}
		}

		// Send as file attachment
		if _, err := s.session.ChannelFileSend(modLogChannelID, "roulette_pairing_report.txt", strings.NewReader(report.String())); err != nil {
			s.config.Logger.Errorf("error sending pairing report file: %v", err)
		}
		return
	}

	_, err := s.session.ChannelMessageSend(modLogChannelID, message)
	if err != nil {
		s.config.Logger.Errorf("Error sending pairing results notification: %v", err)
	}
}

// CheckAndExecuteScheduledPairings checks for due scheduled pairings and executes them
func (s *PairingService) CheckAndExecuteScheduledPairings() {
	scheduledPairings, err := s.db.GetScheduledPairings()
	if err != nil {
		s.config.Logger.Errorf("Error getting scheduled pairings: %v", err)
		return
	}

	if len(scheduledPairings) == 0 {
		return // No scheduled pairings
	}

	s.config.Logger.Infof("Found %d scheduled pairing(s) to execute", len(scheduledPairings))

	for _, schedule := range scheduledPairings {
		s.executeScheduledPairing(schedule)
	}
}

// executeScheduledPairing executes a single scheduled pairing
func (s *PairingService) executeScheduledPairing(schedule database.RouletteSchedule) {
	s.config.Logger.Infof("Executing scheduled pairing for guild %s (scheduled for %s)",
		schedule.GuildID, schedule.ScheduledAt.Format("2006-01-02 15:04:05"))

	// Execute pairing using the pairing service
	result, err := s.ExecutePairing(schedule.GuildID, false)
	if err != nil {
		s.config.Logger.Errorf("Error executing scheduled pairing for guild %s: %v", schedule.GuildID, err)
		s.notifyFailedPairing(schedule.GuildID, fmt.Sprintf("Error executing pairing: %v", err))
		return
	}

	if !result.Success {
		s.config.Logger.Errorf("Scheduled pairing failed for guild %s: %s", schedule.GuildID, result.ErrorMessage)
		s.notifyFailedPairing(schedule.GuildID, result.ErrorMessage)
	} else {
		s.config.Logger.Infof("Successfully executed scheduled pairing for guild %s: %d pair(s) created",
			schedule.GuildID, result.PairCount)

		// Log the results
		s.LogPairingResults(schedule.GuildID, result, true)
	}
}

// notifyFailedPairing sends a notification about failed automated pairing
func (s *PairingService) notifyFailedPairing(guildID, reason string) {
	modLogChannelID := s.config.GetGamerPalsModActionLogChannelID()
	if modLogChannelID == "" {
		return
	}

	message := fmt.Sprintf("⚠️ **Automated Roulette Pairing Failed**\n\n"+
		"**Guild:** %s\n"+
		"**Reason:** %s\n"+
		"**Time:** <t:%d:F>\n\n"+
		"Please check the roulette system and manually trigger pairing if needed.",
		guildID, reason, time.Now().Unix())

	_, err := s.session.ChannelMessageSend(modLogChannelID, message)
	if err != nil {
		s.config.Logger.Errorf("Error sending failed pairing notification: %v", err)
	}
}

// InitializeService initializes the pairing service with a Discord session
func (s *PairingService) InitializeService(session *discordgo.Session) error {
	s.session = session
	return nil
}

// MinuteFuncs returns functions to be called every minute
func (s *PairingService) MinuteFuncs() []func() error {
	return []func() error{
		func() error {
			s.CheckAndExecuteScheduledPairings()
			return nil
		},
	}
}

// HourFuncs returns nil as this service has no hourly tasks
func (s *PairingService) HourFuncs() []func() error {
	return nil
}
