package intro

import (
	"testing"
	"time"

	"github.com/robfig/cron/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExpectedCooldownResetSchedule(t *testing.T) {
	for _, tc := range []struct{ name, zone, expression, expiry, want string }{
		{"exact hour inclusive", "UTC", "@hourly", "2026-09-07T16:00:00Z", "2026-09-07T16:00:00Z"},
		{"second before", "UTC", "@hourly", "2026-09-07T15:59:59Z", "2026-09-07T16:00:00Z"},
		{"second after", "UTC", "@hourly", "2026-09-07T16:00:01Z", "2026-09-07T17:00:00Z"},
		{"fraction after", "UTC", "@hourly", "2026-09-07T16:00:00.000000001Z", "2026-09-07T17:00:00Z"},
		{"local half hour offset", "Asia/Kolkata", "@hourly", "2026-09-07T16:00:01+05:30", "2026-09-07T17:00:00+05:30"},
		{"half hour exact", "Asia/Kolkata", "@hourly", "2026-09-07T16:00:00+05:30", "2026-09-07T16:00:00+05:30"},
		{"spring DST skips hour", "America/Edmonton", "@hourly", "2026-03-08T01:59:59-07:00", "2026-03-08T03:00:00-06:00"},
		{"fall DST repeats hour", "America/Edmonton", "@hourly", "2026-11-01T01:00:01-06:00", "2026-11-01T01:00:00-07:00"},
		{"fall second hour inclusive", "America/Edmonton", "@hourly", "2026-11-01T01:00:00-07:00", "2026-11-01T01:00:00-07:00"},
		{"future quarter hour exact", "Asia/Kolkata", "*/15 * * * *", "2026-09-07T16:15:00+05:30", "2026-09-07T16:15:00+05:30"},
		{"future quarter hour after", "Asia/Kolkata", "*/15 * * * *", "2026-09-07T16:15:01+05:30", "2026-09-07T16:30:00+05:30"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			oldLocal := time.Local
			t.Cleanup(func() { time.Local = oldLocal })
			var err error
			time.Local, err = time.LoadLocation(tc.zone)
			require.NoError(t, err)
			schedule, err := cron.ParseStandard(tc.expression)
			require.NoError(t, err)
			expiry, err := time.Parse(time.RFC3339Nano, tc.expiry)
			require.NoError(t, err)
			want, err := time.Parse(time.RFC3339Nano, tc.want)
			require.NoError(t, err)
			// SQLite persists UTC seconds; cooldowns are elapsed integer hours, including across DST.
			got := expectedCooldownReset(expiry.Add(-48*time.Hour).UTC(), 48, schedule)
			assert.True(t, want.Equal(got), "want %s, got %s", want, got)
		})
	}
}

func TestExpectedCooldownResetScheduleRegistration(t *testing.T) {
	svc := newFeedService(nil)
	jobs := svc.ScheduledFuncs()
	require.Len(t, jobs, 1)
	require.Contains(t, jobs, introCooldownCron)
	schedule, err := cron.ParseStandard(introCooldownCron)
	require.NoError(t, err)
	posted := time.Date(2026, 9, 7, 12, 34, 56, 0, time.UTC)
	assert.Equal(t, expectedCooldownReset(posted, 48, schedule), expectedCooldownReset(posted, 48, introCooldownSchedule))
	assert.True(t, expectedCooldownReset(time.Time{}, 48, introCooldownSchedule).IsZero())
	assert.Equal(t, schedule.Next(posted.In(time.Local).Add(-time.Nanosecond)), expectedCooldownReset(posted, 0, introCooldownSchedule))
}
