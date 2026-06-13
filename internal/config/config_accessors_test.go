package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseBrainRefreshInterval(t *testing.T) {
	const def = 5 * time.Minute
	tests := []struct {
		name      string
		raw       string
		want      time.Duration
		wantValid bool // true when the configured value is used as-is (no fallback)
	}{
		{"unset", "", def, true},
		{"whitespace", "   ", def, true},
		{"valid minutes", "10m", 10 * time.Minute, true},
		{"valid compound", "1h30m", 90 * time.Minute, true},
		{"valid seconds", "30s", 30 * time.Second, true},
		{"trims spaces", "  2m  ", 2 * time.Minute, true},
		{"zero", "0", def, false},
		{"negative", "-5m", def, false},
		{"unparseable", "abc", def, false},
		{"missing unit", "5", def, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := parseBrainRefreshInterval(tt.raw, def)
			require.Equal(t, tt.want, got)
			if tt.wantValid {
				require.Empty(t, reason)
			} else {
				require.NotEmpty(t, reason)
			}
		})
	}
}
