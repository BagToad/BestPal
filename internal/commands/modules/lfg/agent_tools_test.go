package lfg

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeduplicateGameNames(t *testing.T) {
	assert.Equal(t,
		[]string{"Monster Hunter Wilds", "Destiny 2", "Warframe"},
		deduplicateGameNames([]string{
			" Monster Hunter Wilds ",
			"monster hunter wilds",
			"Destiny 2",
			"",
			"Warframe",
			"DESTINY 2",
		}),
	)
}
