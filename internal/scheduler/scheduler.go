package scheduler

import (
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
)

// Scheduler handles periodic execution of scheduled tasks
type Scheduler struct {
	session *discordgo.Session
	config  *config.Config
	db      *database.DB
	mu      sync.RWMutex

	minuteFuncs []func() error
	hourFuncs   []func() error

	minuteTicker *time.Ticker
	hourTicker   *time.Ticker
	minuteStopCh chan struct{}
	hourStopCh   chan struct{}
}

// NewScheduler creates a new scheduler instance
func NewScheduler(session *discordgo.Session, cfg *config.Config, db *database.DB) *Scheduler {
	return &Scheduler{
		session:      session,
		config:       cfg,
		db:           db,
		minuteStopCh: make(chan struct{}),
		hourStopCh:   make(chan struct{}),
	}
}

// RegisterNewMinuteFunc registers a new function to be called every minute.
// returned errors get bubbled up to the log channel and the log file
func (s *Scheduler) RegisterNewMinuteFunc(fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.minuteFuncs = append(s.minuteFuncs, fn)
}

// RegisterNewHourFunc registers a new function to be called every hour.
// returned errors get bubbled up to the log channel and the log file
func (s *Scheduler) RegisterNewHourFunc(fn func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.hourFuncs = append(s.hourFuncs, fn)
}

// Start starts the scheduler
func (s *Scheduler) Start() {
	s.startMinuteScheduler()
	s.startHourScheduler()
}

// Stop stops the scheduler
func (s *Scheduler) Stop() {
	s.stopMinuteScheduler()
	s.stopHourScheduler()
}

// startMinuteScheduler starts a scheduler that runs every minute
func (s *Scheduler) startMinuteScheduler() {
	s.minuteTicker = time.NewTicker(time.Minute)

	go func() {
		s.config.Logger.Info("Minute scheduler started!")

		for {
			select {
			case <-s.minuteTicker.C:

				for _, minuteFunc := range s.minuteFuncs {
					go func() {
						err := minuteFunc()
						if err != nil {
							s.config.Logger.Errorf("Error occurred executing minute func: %v", err)
							err := utils.LogToChannel(s.config, s.session, err.Error())
							if err != nil {
								s.config.Logger.Errorf("Failed to log error to channel: %v", err)
							}
						}
					}()
				}

			case <-s.minuteStopCh:
				s.config.Logger.Info("Minute scheduler stopping")
				return
			}
		}
	}()
}

// stopMinuteScheduler stops the scheduler
func (s *Scheduler) stopMinuteScheduler() {
	if s.minuteTicker != nil {
		s.minuteTicker.Stop()
	}
	close(s.minuteStopCh)
}

func (s *Scheduler) startHourScheduler() {
	s.hourTicker = time.NewTicker(time.Hour)

	go func() {
		s.config.Logger.Info("Hourly scheduler started!")

		for {
			select {
			case <-s.hourTicker.C:

				for _, hourFunc := range s.hourFuncs {
					go func() {
						err := hourFunc()
						if err != nil {
							s.config.Logger.Errorf("Error occurred executing hour func: %v", err)
							err := utils.LogToChannel(s.config, s.session, err.Error())
							if err != nil {
								s.config.Logger.Errorf("Failed to log error to channel: %v", err)
							}
						}
					}()
				}

			case <-s.hourStopCh:
				s.config.Logger.Info("Hourly scheduler stopping")
				return
			}
		}
	}()
}

// stopHourScheduler stops the hourly scheduler
func (s *Scheduler) stopHourScheduler() {
	if s.hourTicker != nil {
		s.hourTicker.Stop()
	}
	close(s.hourStopCh)
}
