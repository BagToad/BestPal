# Architecture Diagram

## Modular Architecture

```
┌─────────────────────────────────────────────────────────────┐
│              modular_handler.go (~230 lines)                 │
│  ┌──────────────────────────────────────────────────────┐  │
│  │ NewModularHandler()                                   │  │
│  │   - Initialize dependencies                           │  │
│  │   - Register all modules                              │  │
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
│  - handler      │  │  - handler      │  │  - handlers     │
│                 │  │                 │  │  service.go     │
└─────────────────┘  └─────────────────┘  └─────────────────┘

┌─────────────────┐  ┌─────────────────┐  ┌─────────────────┐
│ modules/lfg/    │  │ modules/        │  │ modules/game/   │
│  lfg.go         │  │ roulette/       │  │  game.go        │
│  module.go      │  │  roulette.go    │  │                 │
│  now.go         │  │  admin.go       │  │                 │
│                 │  │  module.go      │  │                 │
└─────────────────┘  └─────────────────┘  └─────────────────┘

All logic for each command in its module
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

## Command Execution Flow

```
1. User triggers /command in Discord
   ↓
2. Discord sends interaction to bot
   ↓
3. bot.onInteractionCreate() called
   ↓
4. ModularHandler.HandleInteraction(session, interaction)
   ↓
5. Handler looks up command by name in registry
   ↓
6. Calls Command.HandlerFunc(session, interaction)
   ↓
7. Module's handler executes
   ├── May use module's service
   ├── May use shared dependencies
   └── Sends response to Discord
   ↓
8. Response appears to user
```

## Adding a New Command

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
