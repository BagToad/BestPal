# Copilot Instructions for BestPal

## Project Overview

BestPal is a Discord bot built for the r/GamerPals community. It's a Go-based application that helps gamers connect, find groups, and manage their gaming community on Discord. The bot handles slash commands, user pairing for gaming sessions, IGDB game lookups, and various moderation features.

**Key Facts:**
- Built for a single Discord server (r/GamerPals)
- Written in Go 1.23+
- Uses SQLite for data persistence
- Integrates with Discord API, IGDB (game database), and GitHub Models AI

## Technology Stack

### Core Technologies
- **Language:** Go 1.23 or higher (using Go modules)
- **Discord Library:** `github.com/bwmarrin/discordgo v0.29.0`
- **Configuration:** `github.com/spf13/viper v1.20.1` (YAML + environment variables)
- **Database:** SQLite via `github.com/mattn/go-sqlite3 v1.14.28`
- **Logging:** `github.com/charmbracelet/log v0.4.2`
- **Testing:** `github.com/stretchr/testify v1.10.0`

### External APIs
- **IGDB (Game Database):** For game information lookups
- **GitHub Models:** For AI-powered features
- **Discord API:** For bot interactions

## Project Structure

```
BestPal/
├── cmd/gamerpal/          # Main application entry point
├── internal/
│   ├── bot/               # Bot initialization and session management
│   ├── commands/          # Slash command handlers
│   ├── config/            # Configuration loading and management
│   ├── database/          # SQLite database interactions
│   ├── events/            # Discord event handlers
│   ├── games/             # IGDB game API integration
│   ├── pairing/           # User pairing algorithm for gaming sessions
│   ├── scheduler/         # Task scheduling system
│   ├── utils/             # Utility functions
│   └── welcome/           # New user welcome functionality
├── config.example.yaml    # Example configuration file
├── Makefile              # Build and test commands
└── README.md             # Project documentation
```

## Development Standards

### Code Style
- Follow standard Go conventions and formatting
- Use `gofmt` for code formatting
- Write idiomatic Go code
- Use descriptive variable names
- Keep functions focused and small
- Avoid deep nesting where possible

### Naming Conventions
- Use `PascalCase` for exported functions/types
- Use `camelCase` for unexported functions/types
- Command handlers follow pattern: `handle{CommandName}`
- Test functions: `Test{FunctionName}`

### Error Handling
- Always check and handle errors appropriately
- Use `fmt.Errorf` with `%w` for error wrapping
- Log errors using the configured logger (`cfg.Logger`)
- Return errors to caller when appropriate

### Logging
- Use the centralized logger: `cfg.Logger` (charmbracelet/log)
- Log levels: Debug, Info, Warn, Error, Fatal
- Include context in log messages
- Use `utils.LogToChannel()` for Discord channel logging

## Building and Testing

### Build Commands
```bash
make build      # Build the binary to ./bin/gamerpal
make clean      # Clean build artifacts
make run        # Run the application directly
make test       # Run all tests
make build-all  # Build for multiple platforms
```

### Running Tests
- All tests must pass before committing: `make test`
- Write tests for new functionality
- Follow existing test patterns in `*_test.go` files
- Use table-driven tests where appropriate
- Mock external dependencies (Discord API, IGDB API)

### Test Patterns
```go
// Use newTestConfig() for creating test configurations
func newTestConfig(t *testing.T) *config.Config {
    t.Helper()
    tmpDir := t.TempDir()
    return config.NewMockConfig(map[string]interface{}{
        "bot_token": "test_token",
        "database_path": tmpDir + "/gamerpal.db",
    })
}
```

## Configuration Management

### Configuration Sources (in order of precedence)
1. Environment variables (prefixed with `GAMERPAL_`)
2. YAML configuration file (`config.yaml`)
3. Default values

### Key Configuration Variables
- `GAMERPAL_BOT_TOKEN` / `bot_token`: Discord bot token (required)
  - **Note:** The code binds only `GAMERPAL_BOT_TOKEN` in `bindEnvs()`, but error messages reference both `DISCORD_BOT_TOKEN` and `GAMERPAL_BOT_TOKEN` since the README documents `DISCORD_BOT_TOKEN`.
- `GAMERPAL_IGDB_CLIENT_ID` / `igdb_client_id`: IGDB API client ID
- `GAMERPAL_IGDB_CLIENT_SECRET` / `igdb_client_secret`: IGDB API client secret
- `GAMERPAL_IGDB_CLIENT_TOKEN` / `igdb_client_token`: IGDB access token
- `GAMERPAL_LOG_DIR` / `log_dir`: Directory for log files
- `gamerpals_server_id`: Discord server ID

### Configuration Accessors
- Use getter methods: `cfg.GetBotToken()`, `cfg.GetIGDBClientID()`, etc.
- Never directly access `cfg.v` outside of config package
- See `internal/config/config_accessors.go` for all available getters

## Discord Slash Commands

### Command Structure
Commands are defined in `internal/commands/handler.go` using the `NewSlashCommandHandler()` function.

### Command Categories
1. **Public Commands:** Available to all users (`/ping`, `/help`, `/game`, `/intro`, `/time`, `/lfg`)
2. **Moderator Commands:** Require "Ban Members" permission (`/userstats`, `/say`, `/schedulesay`, `/lfg-admin`)
3. **Administrator Commands:** Require "Administrator" permission (`/prune-inactive`, `/prune-forum`)
4. **Super-Admin Commands:** DM-only, restricted to super admin user IDs (`/config`, `/refresh-igdb`, `/log`)

### Adding New Commands
1. Define the command structure in `NewSlashCommandHandler()` in `internal/commands/handler.go`
2. Create a handler function following the pattern: `func (h *SlashCommandHandler) handle{CommandName}(s *discordgo.Session, i *discordgo.InteractionCreate)`
3. Create a new file: `internal/commands/{commandname}.go`
4. Handle subcommands and options appropriately
5. Always respond to interactions (avoid timeouts)
6. Use ephemeral messages (`discordgo.MessageFlagsEphemeral`) for user-specific responses

### Interaction Response Pattern
```go
err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
    Type: discordgo.InteractionResponseChannelMessageWithSource,
    Data: &discordgo.InteractionResponseData{
        Content: "Response message",
        Flags:   discordgo.MessageFlagsEphemeral, // For private responses
    },
})
```

## Database Interactions

### Database Location
- Default: `./gamerpal.db`
- SQLite3 with `database/sql` interface
- Migrations handled manually (no migration framework)

### Best Practices
- Use prepared statements to prevent SQL injection
- Close rows and statements properly (use `defer`)
- Handle database errors appropriately
- Use transactions for multiple related operations

## Security Considerations

### Sensitive Data
- **Never commit secrets** to the repository
- Keep tokens and API keys in environment variables or `config.yaml` (which is gitignored)
- Use the `/config` command carefully (it shows/modifies sensitive config)
- Token-like values are hidden in config listings

### Permission Checks
- Verify user permissions before executing privileged commands
- Super-admin commands check against `super_admins` list in config
- Moderator commands require appropriate Discord permissions

### Input Validation
- Validate all user inputs
- Sanitize data before database insertion
- Check Discord permission levels
- Handle edge cases and invalid data gracefully

## Common Patterns

### Accessing Configuration
```go
func someFunction(cfg *config.Config) {
    token := cfg.GetBotToken()
    serverID := cfg.GetGamerPalsServerID()
}
```

### Logging
```go
cfg.Logger.Info("Operation started")
cfg.Logger.Errorf("Error occurred: %v", err)
utils.LogToChannel(cfg, session, "Message for Discord log channel")
```

### Command Handler Structure
```go
func (h *SlashCommandHandler) handleMyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Extract options
    options := i.ApplicationCommandData().Options
    
    // Process command logic
    
    // Respond to interaction
    _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
        Type: discordgo.InteractionResponseChannelMessageWithSource,
        Data: &discordgo.InteractionResponseData{
            Content: result,
            Flags:   discordgo.MessageFlagsEphemeral,
        },
    })
}
```

### Testing with Mock Config
```go
func TestMyFunction(t *testing.T) {
    cfg := config.NewMockConfig(map[string]interface{}{
        "bot_token": "test_token",
        "database_path": t.TempDir() + "/test.db",
    })
    
    // Test logic
}
```

## Integration Points

### IGDB (Game Database)
- Client initialization in `internal/commands/handler.go`
- Game lookups via `/game` command
- Handles game information, cover art, release dates, multiplayer modes
- Token refresh via `/refresh-igdb` command

### GitHub Models (AI)
- Client in `internal/utils/models.go`
- Used for AI-powered features
- Requires `github_models_token` configuration

### Discord Features Used
- Slash commands
- Message embeds
- Forum channels
- Thread management
- Ephemeral messages
- Scheduled messages
- User mentions
- Channel categories

## Special Features

### Pairing System (Roulette)
- Algorithm in `internal/pairing/`
- Groups users by common games and regions
- Minimum group size: 4 users
- Creates private channels for paired groups
- Admin commands: `/roulette-admin`

### LFG (Looking for Group)
- Forum-based system for finding gaming groups
- Thread creation with game lookup
- "Looking NOW" feed for active players
- Integration with IGDB for game information

### Scheduled Messages
- Task scheduler in `internal/scheduler/`
- Commands: `/schedulesay`, `/listscheduledsays`, `/cancelscheduledsay`
- Persistent storage in database

### Log Management
- Automatic log rotation
- Prune old logs (7+ days)
- Multi-writer: file + stderr
- Commands: `/log` (view, download, rotate)

## Common Pitfalls to Avoid

1. **Discord API Timeouts:** Always respond to interactions within 3 seconds
2. **Missing Error Checks:** Go requires explicit error handling
3. **Forgetting to Close Resources:** Use `defer` for cleanup
4. **Hardcoded Values:** Use configuration instead
5. **SQL Injection:** Use parameterized queries
6. **Commit Secrets:** Never commit tokens or API keys
7. **Breaking Existing Tests:** Run `make test` before committing
8. **CGO Dependency:** SQLite requires CGO_ENABLED=1 for Linux builds

## Documentation Requirements

- Update README.md for new user-facing commands
- Add examples to USAGE.md for complex commands
- Document configuration changes in config.example.yaml
- Include inline comments for complex logic
- Update this file for significant architectural changes

## Helpful Resources

- [Discord.go Documentation](https://pkg.go.dev/github.com/bwmarrin/discordgo)
- [Discord Developer Portal](https://discord.com/developers/applications)
- [IGDB API Documentation](https://api-docs.igdb.com/)
- [GamerPals Discord](https://discord.gg/gamerpals)

## License

This project is licensed under the MIT License. See LICENSE file for details.
