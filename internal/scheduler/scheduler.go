package scheduler

import (
	"fmt"
	"gamerpal/internal/config"
	"gamerpal/internal/database"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
	"github.com/robfig/cron/v3"
)

// Scheduler handles periodic execution of scheduled tasks using cron
type Scheduler struct {
	session *discordgo.Session
	config  *config.Config
	db      *database.DB
	cron    *cron.Cron
}

// NewScheduler creates a new scheduler instance
func NewScheduler(session *discordgo.Session, cfg *config.Config, db *database.DB) *Scheduler {
	// Create cron with standard 5-field format, skip if still running, and recover from panics
	cronLogger := &cronLogger{logger: cfg.Logger}
	c := cron.New(
		cron.WithChain(
			cron.Recover(cronLogger),
			cron.SkipIfStillRunning(cronLogger),
		),
	)

	return &Scheduler{
		session: session,
		config:  cfg,
		db:      db,
		cron:    c,
	}
}

// RegisterFunc registers a new scheduled function with a cron expression.
// schedule: cron expression (e.g., "@every 1m", "@hourly", "*/5 * * * *")
// name: descriptive name for logging purposes
// fn: function to execute on schedule
func (s *Scheduler) RegisterFunc(schedule, name string, fn func() error) error {
	// Wrap the function to handle errors
	wrappedFunc := func() {
		err := fn()
		if err != nil {
			s.config.Logger.Errorf("Error occurred executing scheduled job '%s': %v", name, err)
			logErr := utils.LogToChannel(s.config, s.session, fmt.Sprintf("Error in scheduled job '%s': %v", name, err))
			if logErr != nil {
				s.config.Logger.Errorf("Failed to log error to channel: %v", logErr)
			}
		}
	}

	_, err := s.cron.AddFunc(schedule, wrappedFunc)
	if err != nil {
		return fmt.Errorf("failed to register scheduled job '%s' with schedule '%s': %w", name, schedule, err)
	}

	s.config.Logger.Infof("Registered scheduled job: %s -> %s", schedule, name)
	return nil
}

// Start starts the scheduler
func (s *Scheduler) Start() {
	s.config.Logger.Info("Cron scheduler starting...")
	s.cron.Start()
	s.config.Logger.Info("Cron scheduler started!")
}

// Stop stops the scheduler gracefully
func (s *Scheduler) Stop() {
	s.config.Logger.Info("Cron scheduler stopping...")
	ctx := s.cron.Stop()
	<-ctx.Done()
	s.config.Logger.Info("Cron scheduler stopped")
}

// cronLogger adapts our config logger to cron's Logger interface
type cronLogger struct {
	logger interface {
		Info(msg interface{}, keyvals ...interface{})
		Error(msg interface{}, keyvals ...interface{})
	}
}

func (l *cronLogger) Info(msg string, keysAndValues ...interface{}) {
	l.logger.Info(msg, keysAndValues...)
}

func (l *cronLogger) Error(err error, msg string, keysAndValues ...interface{}) {
	args := append([]interface{}{"error", err}, keysAndValues...)
	l.logger.Error(msg, args...)
}
