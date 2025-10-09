# Modular Restructuring Proposal

## Executive Summary

This proposal outlines a modular restructuring of the BestPal Discord bot codebase. The current monolithic `handler.go` file (600+ lines) contains all command definitions and delegates to separate service modules. The proposed structure co-locates command definitions with their implementation and services, creating self-contained, maintainable modules.

## Current Architecture Problems

### 1. Scattered Logic
- Command definitions in `handler.go`
- Handlers in separate files (`ping.go`, `say.go`, `time.go`, etc.)
- Services in separate packages (`pairing/`, standalone `schedulesay_service.go`)
- No clear ownership or organization

### 2. Giant Handler File
```
handler.go (667 lines)
├── NewSlashCommandHandler
│   ├── Command definition for lfg (100+ lines)
│   ├── Command definition for roulette (100+ lines)
│   ├── Command definition for say (20+ lines)
│   ├── ... 15+ more commands
├── RegisterCommands
├── HandleInteraction
└── Helper methods
```

### 3. Unclear Service Ownership
- `schedulesay_service.go` in commands package - why?
- `pairing/service.go` in separate package - when should it be separate?
- No consistent pattern

### 4. Difficult Navigation
- Finding the implementation for `/say` requires looking in 3 places
- Adding a new command means modifying the massive handler file
- Testing requires understanding the entire handler

## Proposed Architecture

### Directory Structure

```
internal/commands/
├── types/
│   └── types.go                    # Shared types, interfaces
├── modules/
│   ├── ping/
│   │   └── ping.go                 # Complete ping command
│   ├── time/
│   │   └── time.go                 # Complete time command  
│   ├── say/
│   │   ├── say.go                  # All say commands
│   │   └── service.go              # Say service (scheduling)
│   ├── roulette/
│   │   ├── roulette.go             # Roulette commands
│   │   ├── admin.go                # Roulette admin commands
│   │   └── service.go              # Pairing service
│   ├── lfg/
│   │   ├── lfg.go                  # LFG commands
│   │   └── now.go                  # LFG now functionality
│   ├── game/
│   │   ├── game.go                 # Game lookup command
│   │   └── refresh.go              # IGDB refresh
│   ├── prune/
│   │   └── prune.go                # Prune commands
│   ├── log/
│   │   └── log.go                  # Log commands
│   ├── help/
│   │   └── help.go                 # Help command
│   ├── intro/
│   │   └── intro.go                # Intro command
│   ├── config/
│   │   └── config.go               # Config command
│   └── userstats/
│       └── userstats.go            # User stats command
├── modular_handler.go              # New handler using modules
└── handler.go                      # Legacy (can be removed later)
```

### Core Interfaces

```go
// types/types.go
package types

type Command struct {
    ApplicationCommand *discordgo.ApplicationCommand
    HandlerFunc        func(s *discordgo.Session, i *discordgo.InteractionCreate)
    Development        bool
}

type CommandModule interface {
    Register(commands map[string]*Command, deps *Dependencies)
}

type Dependencies struct {
    Config     *config.Config
    DB         *database.DB
    IGDBClient *igdb.Client
    Session    *discordgo.Session
}
```

### Example Module

```go
// modules/say/say.go
package say

type Module struct {
    service *Service
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.service = NewService(deps.Config)
    
    cmds["say"] = &types.Command{ ... }
    cmds["schedulesay"] = &types.Command{ ... }
    cmds["listscheduledsays"] = &types.Command{ ... }
    cmds["cancelscheduledsay"] = &types.Command{ ... }
}

func (m *Module) GetService() *Service {
    return m.service
}
```

## Benefits

### 1. Co-location of Related Code
All code for a feature lives together:
- `/say` command definition
- Handler functions
- Scheduling service
- All in `modules/say/`

### 2. Clear Service Ownership
- `say.Service` belongs to the say module
- `roulette.Service` belongs to the roulette module
- Services are clearly scoped to their commands

### 3. Maintainability
- Want to modify `/ping`? Go to `modules/ping/ping.go`
- Want to add a new roulette feature? Modify `modules/roulette/`
- No need to touch the handler

### 4. Testability
Each module can be tested independently:
```go
func TestSayModule(t *testing.T) {
    module := &say.Module{}
    // Test in isolation
}
```

### 5. Scalability
Adding a new command:
1. Create `modules/newcmd/newcmd.go`
2. Implement the Module interface
3. Register in `modular_handler.go`
4. Done!

### 6. No Giant Files
- Largest module file: ~200 lines (say.go)
- Average module file: ~100 lines
- vs. Current handler.go: 667 lines

## Migration Strategy

### Phase 1: Foundation (Completed ✅)
- [x] Create types package
- [x] Create module structure
- [x] Implement ModularHandler
- [x] Create example modules (ping, time, say)
- [x] Validate builds and tests

### Phase 2: Simple Commands
Migrate commands with no external dependencies:
- [ ] help
- [ ] intro  
- [ ] config
- [ ] refresh-igdb

### Phase 3: Medium Complexity
Commands with some logic but no services:
- [ ] game (uses IGDB client)
- [ ] userstats (uses database)
- [ ] log (file operations)

### Phase 4: Complex with Services
Commands that have associated services:
- [ ] roulette/roulette-admin (move pairing service)
- [ ] lfg (group into module)
- [ ] prune (combine prune commands)

### Phase 5: Integration
- [ ] Update bot.go to use ModularHandler
- [ ] Update scheduler to use module services
- [ ] Remove legacy handler.go
- [ ] Update documentation

## Backward Compatibility

During migration:
- Both handlers can coexist
- Commands can be migrated incrementally
- No breaking changes to Discord users
- Tests continue to pass

## Implementation Details

### Shared Utilities
Keep in `utils/`:
- `types.go`
- `perms.go`
- `log.go`
- `time.go`
- `colors.go`

These are truly shared across modules.

### External Services
Services used by multiple modules stay separate:
- `database/` - used by multiple modules
- `games/` - IGDB integration
- `scheduler/` - timing infrastructure
- `welcome/` - member onboarding

Module-specific services move with their module:
- `schedulesay_service.go` → `modules/say/service.go`
- `pairing/service.go` → `modules/roulette/service.go`

### Handler Responsibilities

**ModularHandler**:
- Initialize shared dependencies (DB, IGDB client, config)
- Instantiate and register all modules
- Provide command routing (HandleInteraction)
- Expose module services when needed

**Modules**:
- Define command structure
- Implement command handlers
- Manage module-specific services
- Expose services via getters if needed externally

## Testing Strategy

### Unit Tests
Each module should have comprehensive tests:
```go
func TestModule_Register(t *testing.T) { ... }
func TestModule_HandleCommand(t *testing.T) { ... }
func TestModule_Service(t *testing.T) { ... }
```

### Integration Tests
Test module integration with handler:
```go
func TestModularHandler(t *testing.T) {
    handler := NewModularHandler(testConfig)
    // Verify all modules registered
    // Test command routing
}
```

### Regression Tests
Existing tests continue to work during migration.

## Go Best Practices

This structure follows Go idioms:
- ✅ Package naming (lowercase, no underscores)
- ✅ Interface segregation (CommandModule is minimal)
- ✅ Dependency injection (Dependencies struct)
- ✅ No circular dependencies (types package prevents cycles)
- ✅ Clear package boundaries
- ✅ Self-documenting structure

## Performance Considerations

### Minimal Overhead
- Module registration happens once at startup
- No reflection or dynamic loading
- Command routing is still O(1) map lookup

### Memory
- Slightly more memory per module (instance + service)
- Negligible compared to Discord client overhead
- Services are initialized once, not per-command

## Documentation

Created documentation:
- ✅ `docs/MODULAR_STRUCTURE.md` - Comprehensive guide
- ✅ `internal/commands/modules/README.md` - Quick reference
- ✅ Example modules with inline documentation

## Risk Assessment

### Low Risk
- No changes to Discord API integration
- Gradual migration path
- Backward compatible during transition
- All existing tests pass

### Mitigation
- Keep both handlers during migration
- Migrate one module at a time
- Test after each migration
- Easy rollback (just don't use ModularHandler)

## Success Metrics

### Before Migration
- Handler file: 667 lines
- Number of files in commands/: 23
- Average file size: ~200 lines
- Service ownership: unclear

### After Migration  
- Largest module: <250 lines
- Number of module directories: ~15
- Average module size: ~100 lines
- Service ownership: crystal clear

## Recommendation

✅ **Proceed with modular restructuring**

The benefits significantly outweigh the costs:
- Better organization and maintainability
- Clear ownership and boundaries
- Easier onboarding for new contributors
- Follows Go best practices
- Minimal risk with incremental migration

## Next Steps

1. Review and approve this proposal
2. Complete Phase 2 migrations (simple commands)
3. Complete Phase 3 migrations (medium complexity)
4. Complete Phase 4 migrations (complex with services)
5. Switch bot.go to use ModularHandler
6. Remove legacy handler.go
7. Update all documentation

## References

- Implementation: `internal/commands/modular_handler.go`
- Examples: `internal/commands/modules/{ping,time,say}`
- Documentation: `docs/MODULAR_STRUCTURE.md`
- Types: `internal/commands/types/types.go`
