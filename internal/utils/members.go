package utils

import (
	"fmt"
	"hash/fnv"

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

// ObfuscateID generates an obfuscated version of a Discord user ID
func ObfuscateID(userID string, salt string) (string, error) {
	if userID == "" || salt == "" {
		return "", fmt.Errorf("Failed to read salt or something, IDK. Someone should fix it.")
	}

	nouns := []string{
		"Cucumber",
		"Tomato",
		"Banana",
		"Apple",
		"Orange",
		"Strawberry",
		"Blueberry",
		"Raspberry",
		"Kiwi",
		"Peach",
		"Plum",
		"Melon",
		"Grape",
		"Cherry",
		"Papaya",
		"Mango",
		"Watermelon",
	}

	adjectives := []string{
		"Shiny",
		"Juicy",
		"Sweet",
		"Tasty",
		"Fresh",
		"Ripe",
		"Delicious",
		"Colorful",
		"Vibrant",
		"Exotic",
		"Delightful",
		"Succulent",
		"Zesty",
		"Fragrant",
		"Flavorful",
		"Refreshing",
		"Tropical",
	}

	// This isn't the most secure, but should be sufficient for obfuscation.
	// Someone really serious could still reverse engineer it.

	// Use the salt to create a hash within the range of nouns
	hash := fnv.New32a()
	hash.Write([]byte(salt))
	hash.Write([]byte(userID))
	noun := nouns[hash.Sum32()%uint32(len(nouns))]

	// Use the salt to create a hash within the range of adjectives
	hash.Reset()
	hash.Write([]byte(salt))
	hash.Write([]byte(userID))
	adjective := adjectives[hash.Sum32()%uint32(len(adjectives))]

	return fmt.Sprintf("%s %s", adjective, noun), nil
}
