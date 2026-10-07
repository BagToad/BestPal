package intro

import (
	"time"

	"github.com/robfig/cron/v3"
)

// Registration and display must use the same expression and scheduler-local timezone.
const introCooldownCron = "@hourly"

var introCooldownSchedule = func() cron.Schedule {
	schedule, err := cron.ParseStandard(introCooldownCron)
	if err != nil {
		panic(err) // A malformed code-owned schedule is a programming error.
	}
	return schedule
}()

// expectedCooldownReset is the first scheduled reconciliation at or after expiry.
// It is an estimate: downtime or a failed Discord role edit can delay the real unlock.
func expectedCooldownReset(postedAt time.Time, cooldownHours int, schedule cron.Schedule) time.Time {
	if postedAt.IsZero() {
		return time.Time{}
	}
	expiry := postedAt.Add(time.Duration(cooldownHours) * time.Hour)
	// cron.Next is exclusive. The scheduler uses cron.New without WithLocation.
	return schedule.Next(expiry.In(time.Local).Add(-time.Nanosecond))
}
