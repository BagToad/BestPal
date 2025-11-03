# Command Modules

Self-contained command implementations for the BestPal Discord bot.

## Structure

Each module encapsulates:
- Command definition(s)
- Handler logic
- Services (if needed)

## Available Modules

| Module | Commands | Complexity | Features |
|--------|----------|------------|----------|
| **ping** | `/ping` | Simple | Basic response |
| **time** | `/time` | Medium | Date/time parsing, Discord timestamps |
| **say** | `/say`, `/schedulesay`, `/listscheduledsays`, `/cancelscheduledsay` | Complex | Service for scheduled messages |
| **help** | `/help` | Simple | Command documentation |
| **intro** | `/intro`, user app context: `Lookup intro` | Simple | Forum introduction lookup (slash + right-click user) |
| **config** | `/config` | Medium | Bot configuration (SuperAdmin) |
| **refreshigdb** | `/refresh-igdb` | Simple | IGDB token refresh |
| **game** | `/game` | Medium | IGDB game search |
| **userstats** | `/userstats` | Medium | Server statistics |
| **log** | `/log` | Medium | Log file management |
| **prune** | `/prune-inactive`, `/prune-forum` | Complex | User/thread cleanup |
| **lfg** | `/lfg`, `/lfg-admin` | Advanced | Modals, component interactions |
| **roulette** | `/roulette`, `/roulette-admin` | Advanced | Pairing service integration |

## Module Pattern

```go
package mycommand

import "gamerpal/internal/commands/types"

type Module struct {
    config  *config.Config
    service *Service  // Optional
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.config = deps.Config
    
    cmds["mycommand"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "mycommand",
            Description: "Description",
        },
        HandlerFunc: m.handleMyCommand,
    }
}

func (m *Module) handleMyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Implementation
}
```

## Creating a Module

1. Create directory: `mkdir -p modules/mycommand`
2. Create `mycommand.go` implementing `CommandModule`
3. Optional: Create `service.go` for complex logic
4. Register in `modular_handler.go`

See [`/docs/DEVELOPER_GUIDE.md`](../../../docs/DEVELOPER_GUIDE.md) for detailed guide.

## Best Practices

- **Single Responsibility**: Handle related commands only
- **Self-Contained**: Don't depend on other modules
- **Service Co-location**: Keep services with their commands
- **Clean Interfaces**: Expose services via getter methods
- **Documentation**: Comment complex logic

## Testing

```go
func TestModule(t *testing.T) {
    module := &mycommand.Module{}
    commands := make(map[string]*types.Command)
    deps := &types.Dependencies{
        Config: testConfig,
    }
    
    module.Register(commands, deps)
    
    assert.NotNil(t, commands["mycommand"])
}
```
