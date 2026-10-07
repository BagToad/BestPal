package intro

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func TestInteractionUserID(t *testing.T) {
	tests := []struct {
		name string
		in   *discordgo.InteractionCreate
		want string
	}{
		{name: "member user", in: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{Member: &discordgo.Member{User: &discordgo.User{ID: "member-id"}}}}, want: "member-id"},
		{name: "direct user", in: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{User: &discordgo.User{ID: "user-id"}}}, want: "user-id"},
		{name: "missing user", in: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{}}, want: ""},
		{name: "nil interaction", in: nil, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, interactionUserID(tt.in))
		})
	}
}
