package utils

import (
	"github.com/bwmarrin/discordgo"
)

// GetAllGuildMembers fetches all members from a guild (handles pagination)
func GetAllGuildMembers(s *discordgo.Session, guildID string) ([]*discordgo.Member, error) {
	var allMembers []*discordgo.Member
	after := ""

	for {
		members, err := s.GuildMembers(guildID, after, 1000)
		if err != nil {
			return nil, err
		}

		if len(members) == 0 {
			break
		}

		allMembers = append(allMembers, members...)
		after = members[len(members)-1].User.ID
	}

	return allMembers, nil
}
