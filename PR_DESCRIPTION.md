# Modular Command Architecture

## Overview

This PR restructures the Discord bot's command system from a monolithic design to a modular architecture, improving maintainability, scalability, and code organization.

## Problem

The original architecture had significant maintainability issues:
- Single 667-line handler file containing all command definitions
- Command logic scattered across 23+ files
- Unclear service ownership patterns
- Adding new commands required modifying the central handler

## Solution

Implemented a modular command architecture where each command is self-contained:

```
internal/commands/
├── types/              # Shared interfaces and types
├── modules/
│   ├── ping/          # Simple commands
│   ├── time/
│   ├── say/           # Commands with services
│   ├── help/
│   ├── intro/
│   ├── config/
│   ├── refreshigdb/
│   ├── game/
│   ├── userstats/
│   ├── log/
│   ├── prune/
│   ├── lfg/           # Complex with modals/components
│   └── roulette/      # Complex with pairing service
└── modular_handler.go # Lightweight handler
```

## Key Features

**Module Pattern**
- Each module implements the `CommandModule` interface
- Self-registration via `Register()` method
- Services co-located with their commands

**Clean Architecture**
- Shared `types` package prevents import cycles
- Dependency injection via `Dependencies` struct
- Clear separation of concerns

**Handler Simplification**
- Reduced from 667 to ~230 lines
- Routes interactions to appropriate modules
- Exposes services for scheduler integration

## Commands Migrated

All 19 commands now use the modular structure:
- `ping`, `time`, `say`, `schedulesay`, `listscheduledsays`, `cancelscheduledsay`
- `help`, `intro`, `config`, `refresh-igdb`
- `game`, `userstats`, `log`
- `prune-inactive`, `prune-forum`
- `lfg`, `lfg-admin` (with modal/component support)
- `roulette`, `roulette-admin` (with pairing service)

## Benefits

✅ **Maintainability** - Each command isolated in its own module  
✅ **Scalability** - Add commands without touching central handler  
✅ **Testability** - Modules can be tested independently  
✅ **Organization** - Related code lives together  
✅ **Clarity** - Clear service ownership and boundaries

## Metrics

**Before:**
- Handler: 667 lines
- 23 scattered files
- Unclear service ownership

**After:**
- Handler: ~230 lines
- 13 focused modules
- Services co-located with commands
- Net reduction: ~2,700 lines

## Documentation

Comprehensive documentation included:
- `docs/PROPOSAL.md` - Architecture proposal
- `docs/MODULAR_STRUCTURE.md` - Developer guide
- `docs/ARCHITECTURE_DIAGRAM.md` - Visual diagrams
- Module-level README files

## Validation

✅ All tests passing  
✅ Builds successfully  
✅ Zero security vulnerabilities  
✅ Fully backward compatible  
✅ Production ready
