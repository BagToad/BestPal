package permissions

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/assert"
)

func TestHasAdminPermissionsRejectsIncompleteOptions(t *testing.T) {
	assert.False(t, HasAdminPermissions(AdminPermissionsOptions{}))
}

func TestHasModeratorPermissionsRejectsIncompleteOptions(t *testing.T) {
	moderator, err := HasModeratorPermissions(AdminPermissionsOptions{})
	assert.Error(t, err)
	assert.False(t, moderator)
}

func TestHasBanPermissions(t *testing.T) {
	assert.False(t, HasBanPermissions(BanPermissionsOptions{}))
	assert.True(t, HasBanPermissions(BanPermissionsOptions{
		Interaction: &discordgo.InteractionCreate{Interaction: &discordgo.Interaction{
			Member: &discordgo.Member{Permissions: discordgo.PermissionBanMembers},
		}},
	}))
}
