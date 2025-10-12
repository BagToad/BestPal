# Modular Command Architecture

## Overview

The BestPal Discord bot uses a modular command architecture where each command is implemented as a self-contained module. This design improves maintainability, scalability, and code organization.

## Architecture Principles

### 1. Module Pattern
Each command module:
- Implements the `CommandModule` interface
- Contains all related command logic
- Co-locates services with their commands
- Registers itself via the `Register()` method

### 2. Dependency Injection
Shared resources (Config, Database, IGDB Client, Session) are injected via the `Dependencies` struct, avoiding tight coupling and enabling testability.

### 3. No Import Cycles
The `types` package provides shared interfaces and types, preventing circular dependencies between modules and the handler.

## Directory Structure

```
internal/commands/
├── types/
│   └── types.go              # Shared interfaces and types
├── modules/
│   ├── ping/                 # Simple command
│   ├── time/                 # Medium complexity
│   ├── say/                  # Complex with service
│   ├── lfg/                  # Complex with modals/components
│   ├── roulette/             # Complex with pairing service
│   └── ...                   # Other command modules
└── module_handler.go         # Handler that orchestrates modules
```

## Core Components

### Types Package

Defines the contracts that modules must implement:

```go
// Command represents a Discord slash command
type Command struct {
    ApplicationCommand *discordgo.ApplicationCommand
    HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
    Development        bool
}

// CommandModule interface that all modules implement
type CommandModule interface {
    Register(cmds map[string]*Command, deps *Dependencies)
}

// Dependencies provides shared resources to modules
type Dependencies struct {
    Config     *config.Config
    DB         *database.DB
    IGDBClient *igdb.Client
    Session    *discordgo.Session
}
```

### Module Implementation

Each module follows this pattern:

```go
package mycommand

import (
    "gamerpal/internal/commands/types"
    "github.com/bwmarrin/discordgo"
)

type Module struct {
    config  *config.Config
    service *Service  // Optional: if command needs a service
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.config = deps.Config
    
    // Initialize service if needed
    m.service = NewService(deps.Config)
    
    // Register command
    cmds["mycommand"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "mycommand",
            Description: "Description of my command",
        },
        HandlerFunc: m.handleMyCommand,
    }
}

func (m *Module) handleMyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Handle command interaction
}

// Optional: Expose service for external use (e.g., scheduler)
func (m *Module) GetService() *Service {
    return m.service
}
```

### ModuleHandler

The handler orchestrates all modules:

```go
func NewModuleHandler(cfg *config.Config) *ModuleHandler {
    // Initialize shared dependencies
    deps := &types.Dependencies{
        Config:     cfg,
        DB:         database.NewDB(cfg.GetDatabasePath()),
        IGDBClient: igdb.NewClient(...),
    }
    
    // Register all modules
    commands := make(map[string]*types.Command)
    
    pingModule := &ping.Module{}
    pingModule.Register(commands, deps)
    
    timeModule := &time.Module{}
    timeModule.Register(commands, deps)
    
    // ... register other modules
    
    return &ModuleHandler{
        Commands: commands,
        deps:     deps,
        // ... store module references if services needed
    }
}
```

## Command Execution Flow

1. User triggers `/command` in Discord
2. Discord sends interaction to bot
3. `bot.onInteractionCreate()` receives the interaction
4. Calls `ModuleHandler.HandleInteraction()`
5. Handler looks up command in registry
6. Executes `Command.HandlerFunc()`
7. Module's handler processes the request
8. Response sent to Discord

## Creating a New Command

1. **Create module directory**: `mkdir -p internal/commands/modules/newcmd`

2. **Implement the module**:
   ```go
   // internal/commands/modules/newcmd/newcmd.go
   package newcmd
   
   type Module struct {
       config *config.Config
   }
   
   func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
       m.config = deps.Config
       
       cmds["newcmd"] = &types.Command{
           ApplicationCommand: &discordgo.ApplicationCommand{
               Name:        "newcmd",
               Description: "Description",
           },
           HandlerFunc: m.handleNewCmd,
       }
   }
   
   func (m *Module) handleNewCmd(s *discordgo.Session, i *discordgo.InteractionCreate) {
       // Implementation
   }
   ```

3. **Register in handler**:
   ```go
   // internal/commands/module_handler.go
   import "gamerpal/internal/commands/modules/newcmd"
   
   func (h *ModularHandler) registerModules() {
       // ...
       newcmdModule := &newcmd.Module{}
       newcmdModule.Register(h.Commands, h.deps)
   }
   ```

4. **Done!** The command is now available.

## Benefits

- **Maintainability**: Changes are localized to specific modules
- **Scalability**: Adding commands doesn't require modifying a central handler
- **Testability**: Modules can be tested independently
- **Organization**: Related code lives together
- **Clarity**: Clear service ownership and boundaries

## Best Practices

1. **Single Responsibility**: Each module handles related commands only
2. **Self-Contained**: Modules should not depend on other modules
3. **Service Co-location**: Put services in the module that uses them
4. **Clean Interfaces**: Expose services via getter methods when needed externally
5. **Documentation**: Comment complex logic and service interfaces

## Examples

See existing modules for reference:
- **Simple**: `modules/ping/` - Basic command with no dependencies
- **Medium**: `modules/time/` - Command with options and parsing
- **Complex**: `modules/say/` - Multiple commands with service
- **Advanced**: `modules/lfg/` - Modal and component interactions
- **Service Integration**: `modules/roulette/` - External service integration

## Further Reading

- `ARCHITECTURE_DIAGRAM.md` - Visual diagrams of the architecture
- `DEVELOPER_GUIDE.md` - Step-by-step guide for common tasks
- `modules/README.md` - Quick reference for module structure
