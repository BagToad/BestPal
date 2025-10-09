# Modular Command Structure

This document describes the new modular command structure for the BestPal Discord bot and provides a guide for migrating existing commands.

## Overview

The new modular structure organizes commands into self-contained modules where each module contains:
- Command definition(s)
- Handler function(s)  
- Associated service(s) if needed

This approach provides better:
- **Organization**: Related functionality is grouped together
- **Maintainability**: Changes are localized to specific modules
- **Testability**: Modules can be tested in isolation
- **Scalability**: New commands can be added without modifying handler.go

## Architecture

### Directory Structure

```
internal/commands/
├── types/
│   └── types.go              # Shared types and interfaces
├── modules/
│   ├── ping/
│   │   └── ping.go           # Simple command module
│   ├── time/
│   │   └── time.go           # Medium complexity command
│   └── say/
│       ├── say.go            # Command handlers
│       └── service.go        # Associated service
├── modular_handler.go        # New modular command handler
└── handler.go                # Legacy handler (to be phased out)
```

### Core Components

#### 1. Types Package (`internal/commands/types`)

Defines shared interfaces and types:

```go
// Command represents a Discord application command with its handler
type Command struct {
    ApplicationCommand *discordgo.ApplicationCommand
    HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
    Development        bool
}

// CommandModule interface that all modules must implement
type CommandModule interface {
    Register(commands map[string]*Command, deps *Dependencies)
}

// Dependencies contains shared dependencies for command modules
type Dependencies struct {
    Config     *config.Config
    DB         *database.DB
    IGDBClient *igdb.Client
    Session    *discordgo.Session
}
```

#### 2. Command Modules

Each module:
- Implements the `CommandModule` interface
- Contains all logic for its command(s)
- Can include service components
- Registers itself via the `Register()` method

#### 3. Modular Handler

The `ModularHandler` manages command registration and routing:
- Creates shared dependencies
- Instantiates and registers all modules
- Provides access to module services when needed (e.g., for scheduler)

## Module Examples

### Simple Module (Ping)

```go
package ping

import (
    "gamerpal/internal/commands/types"
    "github.com/bwmarrin/discordgo"
)

type Module struct{}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    cmds["ping"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "ping",
            Description: "Check if the bot is responsive",
        },
        HandlerFunc: m.handlePing,
    }
}

func (m *Module) handlePing(s *discordgo.Session, i *discordgo.InteractionCreate) {
    _ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
        Type: discordgo.InteractionResponseChannelMessageWithSource,
        Data: &discordgo.InteractionResponseData{
            Content: "🏓 Pong! Bot is online and responsive.",
        },
    })
}
```

### Module with Service (Say)

```go
package say

import (
    "gamerpal/internal/commands/types"
    // ... other imports
)

// Module with embedded service
type Module struct {
    service *Service
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.service = NewService(deps.Config)
    
    // Register multiple related commands
    cmds["say"] = &types.Command{ /* ... */ }
    cmds["schedulesay"] = &types.Command{ /* ... */ }
    cmds["listscheduledsays"] = &types.Command{ /* ... */ }
    cmds["cancelscheduledsay"] = &types.Command{ /* ... */ }
}

// Expose service for external use (e.g., scheduler)
func (m *Module) GetService() *Service {
    return m.service
}
```

## Migration Guide

### Step 1: Create Module Directory

```bash
mkdir -p internal/commands/modules/yourcommand
```

### Step 2: Create Module File

Create `yourcommand.go` in the new directory:

```go
package yourcommand

import (
    "gamerpal/internal/commands/types"
    "github.com/bwmarrin/discordgo"
)

type Module struct {
    // Add any service instances or state here
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    // Register your command(s)
    cmds["yourcommand"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "yourcommand",
            Description: "Your command description",
            // ... options, permissions, etc.
        },
        HandlerFunc: m.handleYourCommand,
    }
}

func (m *Module) handleYourCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Move handler logic here from handler.go
}
```

### Step 3: Move Associated Services

If your command has a service (like `pairing.Service` or `schedulesay.Service`):

1. Move the service file to the module directory
2. Update package name
3. Update any dependencies or imports
4. Initialize the service in the `Register()` method

### Step 4: Register Module in Handler

Add to `modular_handler.go`:

```go
import (
    "gamerpal/internal/commands/modules/yourcommand"
    // ...
)

func (h *ModularHandler) registerModules() {
    // ... existing modules
    
    yourModule := &yourcommand.Module{}
    yourModule.Register(h.commands, h.deps)
}
```

### Step 5: Remove from Legacy Handler

Once migrated and tested:
1. Remove command definition from `handler.go`
2. Remove handler function from the old file
3. Delete the old file if all commands have been migrated

## Benefits of This Structure

### 1. Encapsulation
Each module is self-contained with all related functionality:
- Command definition
- Handler logic
- Service components
- Module-specific types

### 2. No More Giant Handler File
Instead of 600+ lines in `handler.go`, each command lives in its own focused module.

### 3. Clear Service Ownership
Services are now clearly associated with their commands:
- `say.Service` lives in `modules/say/`
- `roulette` service would live in `modules/roulette/`

### 4. Easy Testing
Modules can be tested independently:
```go
func TestPingModule(t *testing.T) {
    module := &ping.Module{}
    commands := make(map[string]*types.Command)
    deps := &types.Dependencies{ /* test deps */ }
    
    module.Register(commands, deps)
    // Test command registration and handlers
}
```

### 5. Flexible Dependencies
Modules only use what they need from `Dependencies`. Simple commands like `ping` don't need the database or IGDB client.

## Best Practices

### 1. Keep Modules Focused
Each module should represent a logical grouping of related commands. For example:
- `ping` module: just ping
- `say` module: say, schedulesay, listscheduledsays, cancelscheduledsay
- `roulette` module: roulette commands + pairing service

### 2. Use Dependencies Wisely
Only access what you need from `Dependencies`:
```go
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    // Good: Only use what's needed
    m.config = deps.Config
    
    // Avoid: Don't store all deps if not needed
    // m.deps = deps
}
```

### 3. Expose Services Carefully
If a service needs external access (e.g., for scheduler), provide a getter:
```go
func (m *Module) GetService() *Service {
    return m.service
}
```

### 4. Handle Permissions in Module
Define permissions within the module, not in the handler:
```go
var adminPerms int64 = discordgo.PermissionAdministrator

cmds["admin-cmd"] = &types.Command{
    ApplicationCommand: &discordgo.ApplicationCommand{
        DefaultMemberPermissions: &adminPerms,
        // ...
    },
    // ...
}
```

## Migration Priority

Suggested order for migrating remaining commands:

### Phase 1: Simple Commands (No Services)
- [x] ping
- [ ] help  
- [ ] intro
- [ ] config

### Phase 2: Medium Complexity
- [x] time
- [ ] game
- [ ] userstats
- [ ] refresh-igdb

### Phase 3: Commands with Services
- [x] say/schedulesay (with schedulesay service)
- [ ] roulette (with pairing service)
- [ ] lfg (if it has services)

### Phase 4: Complex Commands
- [ ] prune (prune-forum, prune-inactive)
- [ ] log

## Future Enhancements

### 1. Auto-Registration
Consider using init() functions or reflection for automatic module discovery:
```go
func init() {
    RegisterModule(&Module{})
}
```

### 2. Module Lifecycle
Add lifecycle hooks:
```go
type CommandModule interface {
    Register(commands map[string]*types.Command, deps *types.Dependencies)
    Initialize(session *discordgo.Session) error  // Called after bot connects
    Shutdown() error                              // Called on bot shutdown
}
```

### 3. Module Configuration
Allow modules to have their own config sections:
```go
type Module struct {
    config *ModuleConfig
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.config = LoadModuleConfig(deps.Config, "say")
    // ...
}
```

## Testing the New Structure

The modular structure has been tested and validated:
- ✅ All packages build successfully
- ✅ All existing tests pass
- ✅ No import cycles
- ✅ Follows Go idioms and best practices

Example modules provided:
- `ping`: Simple command module
- `time`: Command with options and complex logic
- `say`: Multiple commands with shared service

## Questions?

For questions about this structure or migration help, refer to:
- Example modules in `internal/commands/modules/`
- Type definitions in `internal/commands/types/types.go`
- Implementation in `internal/commands/modular_handler.go`
