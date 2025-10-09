# Command Modules

This directory contains modular command implementations for the BestPal Discord bot.

## Structure

Each subdirectory represents a command module that encapsulates:
- Command definition(s)
- Handler logic
- Associated services (if any)

## Existing Modules

### ping/
Simple command demonstrating the basic module pattern.
- **Commands**: `/ping`
- **Complexity**: Low
- **Services**: None

### time/
Medium complexity command with options and parsing logic.
- **Commands**: `/time`
- **Complexity**: Medium
- **Services**: None
- **Features**: Date/time parsing, Discord timestamp formatting

### say/
Complex module demonstrating command grouping and service integration.
- **Commands**: `/say`, `/schedulesay`, `/listscheduledsays`, `/cancelscheduledsay`
- **Complexity**: High
- **Services**: `say.Service` for scheduled message management
- **Features**: Anonymous messaging, scheduling, in-memory message queue

## Creating a New Module

1. Create a new directory: `mkdir -p modules/mycommand`
2. Create `mycommand.go` implementing the `CommandModule` interface
3. Optionally create `service.go` for complex logic
4. Register the module in `modular_handler.go`

See [`/docs/MODULAR_STRUCTURE.md`](../../../docs/MODULAR_STRUCTURE.md) for detailed migration guide.

## Module Pattern

```go
package mycommand

import (
    "gamerpal/internal/commands/types"
    "github.com/bwmarrin/discordgo"
)

type Module struct {
    // Optional: service instances
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    cmds["mycommand"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "mycommand",
            Description: "My command description",
        },
        HandlerFunc: m.handleMyCommand,
    }
}

func (m *Module) handleMyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Handler implementation
}
```

## Best Practices

1. **Single Responsibility**: Each module should handle related commands only
2. **Self-Contained**: Modules should not depend on other modules
3. **Clean Interfaces**: Expose services via getter methods when needed
4. **Testable**: Write unit tests for each module
5. **Documentation**: Document complex logic and service interfaces

## Testing

Test modules independently:

```go
func TestMyModule(t *testing.T) {
    module := &mycommand.Module{}
    commands := make(map[string]*types.Command)
    deps := &types.Dependencies{
        Config: testConfig,
        // ... other test dependencies
    }
    
    module.Register(commands, deps)
    
    // Assert command was registered
    assert.NotNil(t, commands["mycommand"])
}
```
