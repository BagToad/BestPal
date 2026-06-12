package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Reusable validators for setting descriptors and the panel write path. They
// parse the same way the typed getters do, so a value accepted here resolves
// cleanly later.

// ValidateDuration accepts a Go duration string (e.g. "48h", "30m").
func ValidateDuration(s string) error {
	if _, err := time.ParseDuration(strings.TrimSpace(s)); err != nil {
		return fmt.Errorf("must be a duration like 48h or 30m")
	}
	return nil
}

// ValidateNonNegativeInt accepts an integer >= 0.
func ValidateNonNegativeInt(s string) error {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return fmt.Errorf("must be a whole number")
	}
	if n < 0 {
		return fmt.Errorf("must be zero or greater")
	}
	return nil
}

// ValidateBool accepts a parseable boolean.
func ValidateBool(s string) error {
	if _, err := strconv.ParseBool(strings.TrimSpace(s)); err != nil {
		return fmt.Errorf("must be true or false")
	}
	return nil
}

// ValidateEnum returns a validator that accepts only one of the option values.
func ValidateEnum(opts []Option) func(string) error {
	return func(s string) error {
		for _, o := range opts {
			if o.Value == s {
				return nil
			}
		}
		return fmt.Errorf("must be one of the listed options")
	}
}

// ValidateValue checks a raw string against a setting, using the setting's
// custom Validate if present, otherwise validating by Kind. Picker kinds
// (channel/role/category/list) are sourced from Discord selects and are not
// re-validated here.
func ValidateValue(s Setting, raw string) error {
	if s.Validate != nil {
		return s.Validate(raw)
	}
	switch s.Kind {
	case KindBool:
		return ValidateBool(raw)
	case KindInt:
		return ValidateNonNegativeInt(raw)
	case KindDuration:
		return ValidateDuration(raw)
	case KindEnum:
		return ValidateEnum(s.EnumOptions)(raw)
	default:
		return nil
	}
}
