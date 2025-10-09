# Modular Command Architecture

This directory contains the implementation of a modular command architecture for the BestPal Discord bot.

## Quick Start

### For Users of This Architecture

1. **Read the Proposal**: [`docs/PROPOSAL.md`](../../docs/PROPOSAL.md)
2. **Review the Guide**: [`docs/MODULAR_STRUCTURE.md`](../../docs/MODULAR_STRUCTURE.md)
3. **Study Examples**: Check `modules/{ping,time,say}/`

### For Migrating Commands

1. Create module directory in `modules/yourcommand/`
2. Implement the `CommandModule` interface
3. Register in `modular_handler.go`
4. See migration guide in [`docs/MODULAR_STRUCTURE.md`](../../docs/MODULAR_STRUCTURE.md)

## Architecture Overview

```
┌─────────────────────────────────────────────┐
│           ModularHandler                    │
│  - Manages dependencies                     │
│  - Registers all modules                    │
│  - Routes interactions                      │
└─────────────────┬───────────────────────────┘
                  │
         ┌────────┴─────────┐
         │   Dependencies    │
         │  - Config         │
         │  - Database       │
         │  - IGDB Client    │
         │  - Session        │
         └────────┬──────────┘
                  │
         ┌────────┴─────────────────────┐
         │      Command Modules         │
         │  ┌──────────────────────┐   │
         │  │  ping.Module         │   │
         │  │  time.Module         │   │
         │  │  say.Module          │   │
         │  │    ├── service       │   │
         │  │  roulette.Module     │   │
         │  │    ├── service       │   │
         │  │  ... more modules    │   │
         │  └──────────────────────┘   │
         └──────────────────────────────┘
```

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

### Medium Module (time)
- Single file: `modules/time/time.go`
- Complex logic, no services
- ~170 lines of code
- Shows options handling

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
- [x] Example modules (ping, time, say)
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

All tests pass:
```bash
go test ./...
# All packages: PASS
```

All packages build:
```bash
go build ./...
# No errors
```

## Questions?

See documentation or refer to example modules for guidance.
