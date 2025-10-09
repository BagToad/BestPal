# Architecture Diagram

## Current (Monolithic) Architecture

```
┌─────────────────────────────────────────────────────────────┐
│                    handler.go (667 lines)                    │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ NewSlashCommandHandler()                             │  │
│  │   - Command definitions (15+ commands, ~400 lines)   │  │
│  │   - Initialization logic                             │  │
│  └──────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ RegisterCommands()                                    │  │
│  │ HandleInteraction()                                   │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                            ↓
        ┌──────────────────┼──────────────────┐
        ↓                  ↓                  ↓
┌──────────────┐   ┌──────────────┐   ┌──────────────┐
│  ping.go     │   │  say.go      │   │  time.go     │
│  help.go     │   │  lfg.go      │   │  game.go     │
│  intro.go    │   │  roulette.go │   │  prune.go    │
│  config.go   │   │  etc...      │   │  etc...      │
└──────────────┘   └──────────────┘   └──────────────┘
                            ↓
        ┌──────────────────┼──────────────────┐
        ↓                  ↓                  ↓
┌──────────────────┐  ┌──────────────┐  ┌──────────────┐
│ schedulesay_     │  │ pairing/     │  │ Other        │
│ service.go       │  │ service.go   │  │ services     │
│ (commands/)      │  │ (separate    │  │ scattered    │
│                  │  │  package)    │  │ everywhere   │
└──────────────────┘  └──────────────┘  └──────────────┘

Problem: Logic scattered across multiple locations
```

## New (Modular) Architecture

```
┌─────────────────────────────────────────────────────────────┐
│              modular_handler.go (180 lines)                  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ NewModularHandler()                                   │  │
│  │   - Initialize dependencies                           │  │
│  │   - Call registerModules()                            │  │
│  └──────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ registerModules()                                     │  │
│  │   - Instantiate each module                           │  │
│  │   - Call module.Register()                            │  │
│  └──────────────────────────────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ RegisterCommands() - Routes to Discord                │  │
│  │ HandleInteraction() - Routes to modules               │  │
│  └──────────────────────────────────────────────────────┘  │
└─────────────────────────────────────────────────────────────┘
                            ↓
        ┌──────────────────┼──────────────────┐
        ↓                  ↓                  ↓
┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ modules/ping/   │  │ modules/time/   │  │ modules/say/    │
│  ping.go        │  │  time.go        │  │  say.go         │
│  - Register()   │  │  - Register()   │  │  - Register()   │
│  - handle()     │  │  - handle()     │  │  - handle*()    │
│  (30 lines)     │  │  (170 lines)    │  │  - GetService() │
│                 │  │                 │  │  service.go     │
│                 │  │                 │  │  (520 lines)    │
└─────────────────┘  └─────────────────┘  └─────────────────┘
        ↓                  ↓                  ↓
   [All logic        [All logic         [All logic +
    in one place]     in one place]      service together]

┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ modules/        │  │ modules/        │  │ modules/        │
│ roulette/       │  │ lfg/            │  │ game/           │
│  roulette.go    │  │  lfg.go         │  │  game.go        │
│  admin.go       │  │  now.go         │  │  refresh.go     │
│  service.go     │  │  service.go(?)  │  │                 │
│  (pairing)      │  │                 │  │                 │
└─────────────────┘  └─────────────────┘  └─────────────────┘

Solution: Everything for a command in one module
```

## Dependency Flow

```
┌──────────────────────────────────────────────────────────┐
│                      bot.go                              │
│  Creates: ModularHandler(config)                         │
└────────────────────┬─────────────────────────────────────┘
                     ↓
┌──────────────────────────────────────────────────────────┐
│             ModularHandler                               │
│  Creates Dependencies:                                   │
│    - Config                                              │
│    - Database                                            │
│    - IGDB Client                                         │
│    - Session (set after bot starts)                      │
└────────────────────┬─────────────────────────────────────┘
                     ↓
          ┌──────────┴──────────┐
          ↓                     ↓
┌──────────────────┐   ┌──────────────────┐
│ types.Command    │   │ types.           │
│  - Definition    │   │ Dependencies     │
│  - Handler       │   │  - Config        │
│  - Development   │   │  - DB            │
└──────────────────┘   │  - IGDBClient    │
                       │  - Session       │
                       └──────────────────┘
                              ↓
                    ┌─────────┴─────────┐
                    ↓                   ↓
          ┌──────────────────┐  ┌──────────────────┐
          │ CommandModule    │  │ CommandModule    │
          │  Register()      │  │  Register()      │
          │   - Use deps     │  │   - Use deps     │
          │   - Add commands │  │   - Add commands │
          │   - Init service │  │   - Init service │
          └──────────────────┘  └──────────────────┘
```

## Module Structure

```
modules/mycommand/
│
├── mycommand.go                  # Main module file
│   ├── type Module struct        # Module definition
│   │   └── service *Service      # Optional service instance
│   │
│   ├── func Register()           # Required: Register commands
│   │   ├── Initialize service
│   │   ├── Define command(s)
│   │   └── Add to registry
│   │
│   ├── func handleCommand()      # Command handlers
│   └── func GetService()         # Optional: Expose service
│
└── service.go                    # Optional: Service logic
    ├── type Service struct
    ├── func NewService()
    └── Service methods
```

## Data Flow: Command Execution

```
1. User triggers /command in Discord
   ↓
2. Discord sends interaction to bot
   ↓
3. Bot receives interaction
   ↓
4. bot.onInteractionCreate() called
   ↓
5. ModularHandler.HandleInteraction(session, interaction)
   ↓
6. Handler looks up command by name in registry
   ↓
7. Calls Command.HandlerFunc(session, interaction)
   ↓
8. Module's handler executes
   ├── May use module's service
   ├── May use shared dependencies
   └── Sends response to Discord
   ↓
9. Response appears to user
```

## Comparison: Adding a New Command

### Old Way
```
1. Add definition to handler.go (modify 667-line file)
   ↓
2. Create new file for handler (e.g., newcmd.go)
   ↓
3. If service needed:
   ├── Create service file somewhere
   ├── Decide where to put it (commands/ or separate package?)
   └── Initialize in handler.go
   ↓
4. Update multiple files
```

### New Way
```
1. Create modules/newcmd/
   ↓
2. Create newcmd.go
   ├── Implement Module struct
   ├── Implement Register()
   └── Implement handler(s)
   ↓
3. If service needed:
   └── Create service.go in same directory
   ↓
4. Add 2 lines to modular_handler.go:
   - Import statement
   - Module registration
   ↓
5. Done! All logic in one place
```

## Benefits Visualization

```
                Monolithic                 vs               Modular
    
    ┌─────────────────────┐                    ┌──────────┐
    │     handler.go      │                    │ Module A │
    │      667 lines      │                    │ ~100 loc │
    │                     │                    └──────────┘
    │  Command A def      │                    ┌──────────┐
    │  Command B def      │                    │ Module B │
    │  Command C def      │                    │ ~100 loc │
    │  ...15+ commands    │                    └──────────┘
    └─────────────────────┘                    ┌──────────┐
             ↓↓↓                               │ Module C │
    ┌─────────────────────┐                    │ ~150 loc │
    │  Handler Files      │                    └──────────┘
    │  - ping.go          │                         ...
    │  - say.go           │                    ┌──────────┐
    │  - time.go          │                    │ Module N │
    │  - ...20+ files     │                    │ ~100 loc │
    └─────────────────────┘                    └──────────┘
             ↓↓↓                                    ↓↓↓
    ┌─────────────────────┐              ┌────────────────────┐
    │ Services (scattered)│              │ Services (co-      │
    │ - schedulesay_      │              │  located in        │
    │   service.go        │              │  modules)          │
    │ - pairing/service   │              └────────────────────┘
    │ - Mixed locations   │
    └─────────────────────┘
    
    3 layers, unclear              1 layer, crystal clear
    ownership                      ownership
```

## Summary

### Current Architecture Issues
- 667-line handler file
- Logic scattered across 23+ files
- Unclear service ownership
- Difficult to navigate

### New Architecture Solutions
- Small focused modules (~100-150 lines each)
- All related code together
- Clear service ownership
- Easy to navigate and maintain

### Migration Path
```
Phase 1: Foundation ✅
  - Types package
  - ModularHandler
  - Example modules

Phase 2-4: Incremental Migration
  - Migrate one command at a time
  - Both handlers coexist
  - No breaking changes

Phase 5: Complete
  - All commands migrated
  - Remove legacy handler
  - Clean, modular codebase
```
