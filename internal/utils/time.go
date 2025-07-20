package utils

import (
	"fmt"
	"strings"

	"github.com/markusmobius/go-dateparser"
)

// ParseUnixTimestamp attempts to parse a date/time string and return a unix timestamp.
func ParseUnixTimestamp(dateString string) (int64, error) {
	dateString = strings.TrimSpace(dateString)

	dt, err := dateparser.Parse(nil, dateString)
	if err != nil {
		return 0, fmt.Errorf("unable to parse date/time format: %w", err)
	}

	timestamp := dt.Time.Unix()
	return timestamp, nil
}
