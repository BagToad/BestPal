# Best Pal

Built for [r/GamerPals](https://www.reddit.com/r/GamerPals).

## Project Structure

This project follows Go's standard directory layout:

```
gamerpal/
├── cmd/
│   └── gamerpal/           # Main application entry point
│       └── main.go
├── internal/               # Private application code
│   ├── bot/               # Bot core functionality
│   │   └── bot.go
│   ├── commands/          # Slash command handlers
│   │   ├── handler.go     # Command registration and routing
│   │   ├── help.go        # Help command
│   │   ├── ping.go        # Ping command
│   │   ├── prune.go       # Prune inactive users command
│   │   └── userstats.go   # User statistics command
│   ├── config/            # Configuration management
│   │   ├── config.go      # Configuration loading
│   │   └── config_test.go # Configuration tests
│   └── utils/             # Internal utility functions
│       ├── members.go     # Member management utilities
│       ├── perms.go       # Permission checking utilities
│       └── types.go       # Type helper functions
├── pkg/                   # Public library code (currently empty)
├── bin/                   # Compiled binaries (gitignored)
├── .env                   # Environment variables (gitignored)
├── .env.example          # Environment variables template
├── Dockerfile            # Docker configuration
├── Makefile             # Build automation
├── LICENSE              # MIT License
├── USAGE.md             # Usage documentation
└── go.mod               # Go module definition
```

## Features

- ⚡ **Modern Slash Commands**: Uses Discord's application commands (slash commands) for the best user experience
- 📊 **User Statistics**: Comprehensive server statistics including member counts, growth metrics, and regional breakdowns
- 🔨 **User Management**: Remove inactive users (those without roles) with safety features
- 🏓 **Ping Command**: Check bot responsiveness
- 📚 **Help System**: Easy-to-use help command for all available features
- 🔧 **Auto-Registration**: Automatically registers and unregisters commands
- 🎭 **Dynamic Status**: Bot cycles through different status messages
- 🛡️ **Permission System**: Role-based and administrator permission checking
- 🐳 **Docker Ready**: Containerized deployment

## Slash Commands

| Command | Description | Permissions |
|---------|-------------|-------------|
| `/userstats` | Shows member counts, growth metrics, and regional breakdown | Admin roles or Administrator permission |
| `/ping` | Check if the bot is responsive | Admin roles or Administrator permission |
| `/prune-inactive` | Remove users without any roles (dry run by default) | Administrator permission |
| `/help` | Display all available commands | Admin roles or Administrator permission |

## Quick Start

### Prerequisites

- Go 1.23 or higher
- A Discord bot token (see [Discord Developer Portal](https://discord.com/developers/applications))

### Installation

1. Clone the repository:
   ```bash
   git clone https://github.com/bagtoad/gamerpal.git
   cd gamerpal
   ```

2. Install dependencies:
   ```bash
   make deps
   ```

3. Configure your bot:
   ```bash
   cp .env.example .env
   # Edit .env with your bot token
   ```

4. Build and run:
   ```bash
   make run
   ```

## Development

### Building

```bash
# Build the application
make build

# Build for multiple platforms
make build-all

# Clean build artifacts
make clean
```

### Running

```bash
# Run the bot
make run
```

### Testing

```bash
# Run tests
make test
```

### Docker

```bash
# Build Docker image
make docker-build

# Run with Docker
make docker-run
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


## Important Notes

- **Slash Commands**: This bot uses Discord's modern slash commands system. Commands appear in the Discord command picker when you type `/`
- **Auto-Registration**: Commands are automatically registered when the bot starts and unregistered when it shuts down
- **Global Commands**: Commands are registered globally and may take up to 1 hour to appear in all servers

## Adding New Commands

To add a new slash command:

1. Add the command definition to `RegisterCommands()` in `internal/commands/handler.go`
2. Add a case for the command in `HandleInteraction()`
3. Create a new file in `internal/commands/` with your command handler (following the pattern `handle{CommandName}`)
4. Implement permission checking if needed using utilities from `internal/utils/perms.go`
5. The command will be automatically registered when the bot starts

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Run tests and ensure code quality
5. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.

## Support

For support, please create an issue in the GitHub repository.
