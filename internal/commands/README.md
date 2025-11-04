# Modular Command Architecture

The BestPal Discord bot uses a modular command architecture for better maintainability and scalability.

## Quick Reference

- **Architecture**: See [`docs/ARCHITECTURE.md`](../../docs/ARCHITECTURE.md)
- **Developer Guide**: See [`docs/DEVELOPER_GUIDE.md`](../../docs/DEVELOPER_GUIDE.md)
- **Visual Diagrams**: See [`docs/ARCHITECTURE_DIAGRAM.md`](../../docs/ARCHITECTURE_DIAGRAM.md)
- **Module Reference**: See [`modules/README.md`](modules/README.md)

## Structure

```
internal/commands/
├── types/              # Shared interfaces and types
├── modules/           # Self-contained command modules
│   ├── ping/
│   ├── say/          # With service
│   └── ...
└── modular_handler.go # Routes commands to modules
```

## Core Concepts

### CommandModule Interface
All modules implement this interface:
```go
type CommandModule interface {
    Register(cmds map[string]*Command, deps *Dependencies)
}
```

### Dependencies
Shared resources injected into modules:
```go
type Dependencies struct {
    Config     *config.Config
    DB         *database.DB
    IGDBClient *igdb.Client
    Session    *discordgo.Session
}
```

### Module Pattern
Each module is self-contained:
- Command definition(s)
- Handler function(s)
- Service (if needed)

## Examples

| Module | Complexity | Description |
|--------|------------|-------------|
| `ping/` | Simple | Basic command |
| `say/` | Complex | Multiple commands with service |
| `lfg/` | Advanced | Modal and component interactions |
| `roulette/` | Advanced | External service integration |

## Adding a Command

1. Create module directory: `mkdir -p modules/mycommand`
2. Implement `CommandModule` interface
3. Register in `modular_handler.go`

See [`docs/DEVELOPER_GUIDE.md`](../../docs/DEVELOPER_GUIDE.md) for detailed instructions.

## Architecture Benefits

- **Maintainable**: Changes are localized to modules
- **Scalable**: Add commands without touching central handler
- **Testable**: Modules test independently
- **Organized**: Related code lives together
- **Clear**: Explicit service ownership

## Key Components

### Types Package (`types/`)
Defines shared interfaces and types that modules use.

### Modules (`modules/`)
Self-contained command implementations. Each module:
- Defines its command(s)
- Implements handlers
- Manages services (if needed)
- Registers with the handler

### Modular Handler (`modular_handler.go`)
New handler that uses the module system. Replaces the legacy monolithic handler.

### Legacy Handler (`handler.go`)
Original 600+ line handler. Being phased out as commands migrate.

## Module Examples

### Simple Module (ping)
- Single file: `modules/ping/ping.go`
- No services
- ~30 lines of code
- Shows basic pattern

### (Removed) Time Module
The former `time` module has been deprecated and marked development-only for automatic unregistration. Its date parsing helper remains available in `internal/utils/time.go` for any future features.

### Complex Module (say)
- Multiple files: `say.go`, `service.go`
- Includes service for scheduling
- ~400 lines total
- Shows service integration

## Migration Status

### ✅ Completed
- [x] Architecture design
- [x] Types package
- [x] ModularHandler implementation
- [x] Example modules (ping, say)
- [x] Documentation
- [x] Tests passing

### 📋 Remaining

#### Phase 2: Simple Commands
- [ ] help
- [ ] intro
- [ ] config
- [ ] refresh-igdb

#### Phase 3: Medium Complexity
- [ ] game
- [ ] userstats
- [ ] log

#### Phase 4: Complex with Services
- [ ] roulette (with pairing service)
- [ ] lfg
- [ ] prune

#### Phase 5: Integration
- [ ] Switch bot.go to ModularHandler
- [ ] Update scheduler integration
- [ ] Remove legacy handler
- [ ] Final documentation update

## Benefits

✅ **Organization**: Related code lives together  
✅ **Maintainability**: Changes are localized  
✅ **Scalability**: Easy to add new commands  
✅ **Testability**: Modules test independently  
✅ **Clarity**: Clear service ownership  
✅ **Best Practices**: Follows Go idioms  

## Documentation

- **[PROPOSAL.md](../../docs/PROPOSAL.md)**: Detailed proposal and rationale
- **[MODULAR_STRUCTURE.md](../../docs/MODULAR_STRUCTURE.md)**: Complete migration guide
- **[modules/README.md](modules/README.md)**: Quick module reference

## Testing

All tests pass with the new modular structure:
```bash
go test ./...
# All packages: PASS
```

All packages build successfully:
```bash
go build ./...
# No errors
```

**Note**: The modular structure is currently implemented alongside the existing handler. Full integration (switching bot.go to use ModularHandler) is pending but the foundation is complete and tested.

## Questions?

See documentation or refer to example modules for guidance.
