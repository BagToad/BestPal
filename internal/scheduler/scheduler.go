package scheduler

import (
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/pairing"
	"log"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Scheduler handles periodic execution of scheduled tasks
type Scheduler struct {
	session        *discordgo.Session
	config         *config.Config
	db             *database.DB
	pairingService *pairing.PairingService
	ticker         *time.Ticker
	stopCh         chan struct{}
}

// NewScheduler creates a new scheduler instance
func NewScheduler(session *discordgo.Session, cfg *config.Config, db *database.DB, pairingService *pairing.PairingService) *Scheduler {
	return &Scheduler{
		session:        session,
		config:         cfg,
		db:             db,
		pairingService: pairingService,
		stopCh:         make(chan struct{}),
	}
}

// Start begins the scheduler's background operations
func (s *Scheduler) Start() {
	// Check for scheduled pairings every minute
	s.ticker = time.NewTicker(time.Minute)

	go func() {
		log.Println("Scheduler started - checking for scheduled pairings every minute")

		for {
			select {
			case <-s.ticker.C:
				s.checkAndExecuteScheduledPairings()
			case <-s.stopCh:
				log.Println("Scheduler stopping")
				return
			}
		}
	}()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.stopCh)
}

// checkAndExecuteScheduledPairings checks for due scheduled pairings and executes them
func (s *Scheduler) checkAndExecuteScheduledPairings() {
	scheduledPairings, err := s.db.GetScheduledPairings()
	if err != nil {
		log.Printf("Error getting scheduled pairings: %v", err)
		return
	}

	if len(scheduledPairings) == 0 {
		return // No scheduled pairings
	}

	log.Printf("Found %d scheduled pairing(s) to execute", len(scheduledPairings))

	for _, schedule := range scheduledPairings {
		s.executeScheduledPairing(schedule)
	}
}

// executeScheduledPairing executes a single scheduled pairing
func (s *Scheduler) executeScheduledPairing(schedule database.RouletteSchedule) {
	log.Printf("Executing scheduled pairing for guild %s (scheduled for %s)",
		schedule.GuildID, schedule.ScheduledAt.Format("2006-01-02 15:04:05"))

	// Execute pairing using the pairing service
	result, err := s.pairingService.ExecutePairing(schedule.GuildID, false)
	if err != nil {
		log.Printf("Error executing scheduled pairing for guild %s: %v", schedule.GuildID, err)
		s.notifyFailedPairing(schedule.GuildID, fmt.Sprintf("Error executing pairing: %v", err))
		return
	}

	if !result.Success {
		log.Printf("Scheduled pairing failed for guild %s: %s", schedule.GuildID, result.ErrorMessage)
		s.notifyFailedPairing(schedule.GuildID, result.ErrorMessage)
	} else {
		log.Printf("Successfully executed scheduled pairing for guild %s: %d pair(s) created",
			schedule.GuildID, result.PairCount)

		// Log the results
		s.pairingService.LogPairingResults(schedule.GuildID, result, true)
	}
}

// notifyFailedPairing sends a notification about failed automated pairing
func (s *Scheduler) notifyFailedPairing(guildID, reason string) {
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
		log.Printf("Error sending failed pairing notification: %v", err)
	}
}
