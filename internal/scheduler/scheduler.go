package scheduler

import (
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/pairing"
	"gamerpal/internal/welcome"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Scheduler handles periodic execution of scheduled tasks
type Scheduler struct {
	session        *discordgo.Session
	config         *config.Config
	db             *database.DB
	pairingService *pairing.PairingService
	welcomeService *welcome.WelcomeService
	minuteTicker   *time.Ticker
	hourTicker     *time.Ticker
	minuteStopCh   chan struct{}
	hourStopCh     chan struct{}
}

// NewScheduler creates a new scheduler instance
func NewScheduler(session *discordgo.Session, cfg *config.Config, db *database.DB, pairingService *pairing.PairingService) *Scheduler {
	return &Scheduler{
		session:        session,
		config:         cfg,
		db:             db,
		pairingService: pairingService,
		welcomeService: welcome.NewWelcomeService(session, cfg),
		minuteStopCh:   make(chan struct{}),
		hourStopCh:     make(chan struct{}),
	}
}

// StartMinuteScheduler starts a scheduler that runs every minute
func (s *Scheduler) StartMinuteScheduler() {
	// Check for scheduled pairings every minute
	s.minuteTicker = time.NewTicker(time.Minute)

	go func() {
		s.config.Logger.Info("Minute scheduler started!")

		for {
			select {
			case <-s.minuteTicker.C:
				s.config.Logger.Info("Running scheduled tasks...")
				fmt.Printf("Checking welcome service at %s...\n", time.Now().Format(time.RFC1123))
				go func() {
					s.config.Logger.Info("Checking welcome service...")
					s.welcomeService.CleanNewPalsRoleFromOldMembers()
					s.welcomeService.CheckAndWelcomeNewPals()
				}()
				s.checkAndExecuteScheduledPairings()
			case <-s.minuteStopCh:
				s.config.Logger.Info("Minute scheduler stopping")
				return
			}
		}
	}()
}

// StopMinuteScheduler stops the scheduler
func (s *Scheduler) StopMinuteScheduler() {
	if s.minuteTicker != nil {
		s.minuteTicker.Stop()
	}
	close(s.minuteStopCh)
}

func (s *Scheduler) StartHourScheduler() {
	// Check for old log files every hour
	s.hourTicker = time.NewTicker(time.Hour)

	go func() {
		s.config.Logger.Info("Hourly scheduler started!")

		for {
			select {
			case <-s.hourTicker.C:
				if err := s.config.RotateAndPruneLogs(); err != nil {
					s.config.Logger.Errorf("Scheduler failed handling log files: %v", err)
				}
			case <-s.hourStopCh:
				s.config.Logger.Info("Hourly scheduler stopping")
				return
			}
		}
	}()
}

// StopHourScheduler stops the hourly scheduler
func (s *Scheduler) StopHourScheduler() {
	if s.hourTicker != nil {
		s.hourTicker.Stop()
	}
	close(s.hourStopCh)
}
