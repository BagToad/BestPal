package scheduler

import (
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
	ticker         *time.Ticker
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
	s.ticker = time.NewTicker(time.Minute)

	go func() {
		s.config.Logger.Info("Minute scheduler started!")

		for {
			select {
			case <-s.ticker.C:
				go func() {
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
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.minuteStopCh)
}

func (s *Scheduler) StartHourScheduler() {
	// Check for old log files every hour
	s.ticker = time.NewTicker(time.Hour)

	go func() {
		s.config.Logger.Info("Hourly scheduler started!")

		for {
			select {
			case <-s.ticker.C:
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
	if s.ticker != nil {
		s.ticker.Stop()
	}
	close(s.hourStopCh)
}
