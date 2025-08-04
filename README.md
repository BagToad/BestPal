# Best Pal

Built for [r/GamerPals](https://www.reddit.com/r/GamerPals). Join the discord at [discord.gg/gamerpals](https://discord.gg/gamerpals).

It's a work in progress, and probably always will be.

Note that this bot is coded to only work with a single server per bot instance since channel IDs are all set in a
global config file.

## Slash Commands

| Command | Description | Permissions |
|---------|-------------|-------------|
| `/userstats` | Shows member counts, growth metrics, and regional breakdown | Admin roles or Administrator permission |
| `/ping` | Check if the bot is responsive | Admin roles or Administrator permission |
| `/prune-inactive` | Remove users without any roles (dry run by default) | Administrator permission |
| `/say` | Send an anonymous message to a specified channel | Administrator permission |
| `/help` | Display all available commands | Admin roles or Administrator permission |

## Quick Start

### Prerequisites

- Go 1.23 or higher
- A Discord bot token (see [Discord Developer Portal](https://discord.com/developers/applications))

## Development

### Building

1. Clone the repository:
   ```bash
   git clone https://github.com/bagtoad/bestpal.git
   cd bestpal
   ```

2. Configure your bot:
   ```bash
   cp config.example.yaml config.yaml
   # Edit config.yaml with your bot token
   ```

3. Build and run:
   ```bash
   make run
   ```

## Configuration

The bot uses environment variables for configuration:

| Variable | Description | Default |
|----------|-------------|---------|
| `DISCORD_BOT_TOKEN` | Your Discord bot token | *required* |

## Bot Permissions

The bot requires the following permissions:
- ✅ **Use Slash Commands**
- ✅ **View Channels**
- ✅ **Send Messages**
- ✅ **Read Server Members** (for user statistics and prune functionality)
- ✅ **Kick Members** (for prune functionality)

## Adding New Commands

To add a new slash command:

1. Add the command definition to `NewSlashHandler()` in `internal/commands/handler.go`
2. Create a new file in `internal/commands/` with your command handler (following the pattern `handle{CommandName}`)

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

For support, make a ticket in [GamerPals Discord server](https://discord.gg/gamerpals) or open an issue on GitHub.
