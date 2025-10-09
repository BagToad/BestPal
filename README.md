# Best Pal

Built for [r/GamerPals](https://www.reddit.com/r/GamerPals). Join the discord at [discord.gg/gamerpals](https://discord.gg/gamerpals).

It's a work in progress, and probably always will be.

Note that this bot is coded to only work with a single server per bot instance since channel IDs are all set in a
global config file.

## Slash Commands

### Public Commands (Everyone)
| Command | Description |
|---------|-------------|
| `/ping` | Check if the bot is responsive |
| `/intro` | Look up a user's latest introduction post from the introductions forum |
| `/help` | Display all available commands |
| `/game` | Look up information about a video game from IGDB |
| `/time` | Time-related utilities for converting dates to Discord timestamps |
| `/lfg now` | Mark yourself as looking now inside an LFG thread |

### Moderator Commands (Ban Members Permission)
| Command | Description |
|---------|-------------|
| `/userstats` | Show member statistics for the server |
| `/say` | Send an anonymous message to a specified channel |
| `/schedulesay` | Schedule an anonymous message to be sent later |
| `/listscheduledsays` | List the next 20 scheduled messages |
| `/cancelscheduledsay` | Cancel a scheduled message by ID |
| `/lfg-admin setup-find-a-thread` | Set up the LFG find-a-thread panel |
| `/lfg-admin setup-looking-now` | Set up the 'Looking NOW' feed channel |
| `/lfg-admin refresh-thread-cache` | Rebuild the LFG thread cache |

### Administrator Commands
| Command | Description |
|---------|-------------|
| `/prune-inactive` | Remove users without any roles (dry run by default) |
| `/prune-forum` | Scan a forum for threads whose starter post was deleted (dry-run by default) |

### Super-Admin Commands (DM Only)
| Command | Description |
|---------|-------------|
| `/config` | View or modify the bot configuration |
| `/refresh-igdb` | Refresh the IGDB client token |
| `/log` | Log file management commands |

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
