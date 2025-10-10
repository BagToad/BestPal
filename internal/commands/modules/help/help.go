package help

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/utils"

	"github.com/bwmarrin/discordgo"
)

// Module implements the CommandModule interface for the help command
type HelpModule struct{}

// New creates a new help module
func New() *HelpModule {
	return &HelpModule{}
}

// Register adds the help command to the command map
func (m *HelpModule) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
	cmds["help"] = &types.Command{
		ApplicationCommand: &discordgo.ApplicationCommand{
			Name:        "help",
			Description: "Show all available commands",
		},
		HandlerFunc: m.handleHelp,
	}
}

// handleHelp handles the help slash command
func (m *HelpModule) handleHelp(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "🎮 Best Pal Bot - Help",
		Description: "A bot for r/GamerPals. Check out the code on [GitHub](https://github.com/BagToad/BestPal)",
		Color:       utils.Colors.Info(),
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "🤖 Available Commands:",
				Inline: false,
			},
			{
				Name:   "/ping",
				Value:  "Check if the bot is responsive",
				Inline: false,
			},
			{
				Name:   "/game",
				Value:  "Look up information about a video game from IGDB\n• Use `/game name:GameName` to search for a game",
				Inline: false,
			},
			{
				Name:   "/intro",
				Value:  "Look up a user's latest introduction post\n• Use `/intro` to find your own introduction\n• Use `/intro user:@username` to find someone else's introduction",
				Inline: false,
			},
			{
				Name:   "/time",
				Value:  "Time-related utilities\n• Use `/time datetime:2025-08-25 3:00 PM` to convert dates to Discord timestamps\n• Use `full:true` to see all Discord timestamp formats",
				Inline: false,
			},
			{
				Name:   "/lfg now",
				Value:  "Mark yourself as looking for group in an LFG thread\n• Use `/lfg now region:Region message:Text player_count:X` to post",
				Inline: false,
			},
			{
				Name:   "/help",
				Value:  "Show this help message",
				Inline: false,
			},
			{
				Name:   "/roulette help",
				Value:  "Show detailed help for roulette pairing commands",
				Inline: false,
			},
			{
				Name:   "🛠️ Moderator Commands:",
				Inline: false,
			},
			{
				Name:   "/userstats",
				Value:  "Show member statistics for the server\n• Use `stats:overview` or `stats:daily` for different views",
				Inline: false,
			},
			{
				Name:   "/say",
				Value:  "Send an anonymous message to a specified channel\n• Use `/say channel:#general message:Hello everyone!` to send a message",
				Inline: false,
			},
			{
				Name:   "/schedulesay",
				Value:  "Schedule an anonymous message to be sent later\n• Use `/schedulesay channel:#general message:Text timestamp:123456789` to schedule",
				Inline: false,
			},
			{
				Name:   "/listscheduledsays",
				Value:  "List the next 20 scheduled messages",
				Inline: false,
			},
			{
				Name:   "/cancelscheduledsay",
				Value:  "Cancel a scheduled message by ID\n• Use `/cancelscheduledsay id:123` to cancel",
				Inline: false,
			},
			{
				Name:   "/lfg-admin",
				Value:  "LFG admin commands\n• `/lfg-admin setup-find-a-thread` - Set up find-a-thread panel\n• `/lfg-admin setup-looking-now` - Set up Looking NOW feed channel\n• `/lfg-admin refresh-thread-cache` - Rebuild thread cache",
				Inline: false,
			},
			{
				Name:   "🚀 Admin Commands:",
				Inline: false,
			},
			{
				Name:   "/prune-inactive",
				Value:  "Remove users without any roles (dry run by default)\n• Use `execute:true` to actually remove users",
				Inline: false,
			},
			{
				Name:   "/prune-forum",
				Value:  "Find forum threads whose starter post was deleted\n• Use `forum:#channel execute:true` to delete threads",
				Inline: false,
			},
			{
				Name:   "/roulette-admin help",
				Value:  "Show detailed help for roulette admin commands",
				Inline: false,
			},
		},
	}

	// Respond immediately with the embed
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: []*discordgo.MessageEmbed{embed},
		},
	})
}

// GetServices returns nil as this module has no services requiring initialization
func (m *HelpModule) GetService() types.ModuleService {
return nil
}
