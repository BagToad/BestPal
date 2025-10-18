# Best Pal

Discord bot for the [r/GamerPals](https://www.reddit.com/r/GamerPals) community.
Join: [discord.gg/gamerpals](https://discord.gg/gamerpals)

Modular architecture: each feature lives in `internal/commands/modules/<name>`.
Single‑guild by design (`config.yaml` holds IDs).

## Slash Commands

### Public (Everyone)
| Command | Description |
|---------|-------------|
| `/ping` | Bot health check |
| `/help` | List available commands |
| `/intro` | Find a user's intro forum post |
| `/game` | IGDB game lookup |
| `/time` | Convert dates → Discord timestamps |
| `/game-thread` | Autocomplete search for LFG game threads |
| `/lfg now` | Mark yourself as "Looking NOW" inside an LFG thread |
| `/roulette signup` | Sign up for roulette pairing |
| `/roulette nah` | Remove yourself from pairing |
| `/roulette games-add` | Add games to your pairing list |
| `/roulette games-remove` | Remove games from your pairing list |
| `/roulette help` | Show roulette help |

### Moderator (Ban Members Permission)
| Command | Description |
|---------|-------------|
| `/say` | Send an anonymous message to a channel |
| `/schedulesay` | Schedule an anonymous message |
| `/listscheduledsays` | List next scheduled messages |
| `/cancelscheduledsay` | Cancel a scheduled message by ID |
| `/lfg setup-find-a-thread` | Set up the LFG find-a-thread panel |
| `/lfg setup-looking-now` | Set up the "Looking NOW" feed channel |
| `/lfg refresh-thread-cache` | Rebuild LFG thread cache (includes archived) |
| `/userstats` | Show server member statistics |

### Administrator (Administrator Permission)
| Command | Description |
|---------|-------------|
| `/prune-inactive` | Remove users with no roles (dry-run by default) |
| `/prune-forum` | Scan a forum for threads whose starter post was deleted (dry-run by default) |
| `/roulette-admin help` | Show admin roulette help |
| `/roulette-admin debug` | Debug info about roulette system |
| `/roulette-admin pair` | Schedule or execute pairing (supports time / immediate / dryrun) |
| `/roulette-admin simulate-pairing` | Simulate pairing with fake users |
| `/roulette-admin reset` | Delete all existing pairing channels |
| `/roulette-admin delete-schedule` | Remove the scheduled pairing time |

### Super-Admin (DM Only; IDs listed in `config.yaml`)
| Command | Description |
|---------|-------------|
| `/config` | View / modify bot configuration |
| `/refresh-igdb` | Refresh IGDB API token |
| `/log` | Retrieve / manage bot logs |

### Service / Background Modules (No direct slash commands)
| Module | Purpose |
|--------|---------|
| `welcome` | Scheduled member welcome tasks |
| `say` | Dispatch scheduled anonymous messages |
| `roulette` | Automated pairing execution when scheduled |

## Quick Start

### Prerequisites
| Item | Why |
|------|-----|
| Go 1.23+ | Build & run the bot |
| Discord bot token | Auth to Discord API |
| IGDB client id/secret | Game lookup & metadata features |

### Adding a Module (Summary)
Create `internal/commands/modules/<name>/`, implement `New` + `Register`, optional `Service()`, then add to `registerModules()`.

## Architecture

Reference docs: `docs/ARCHITECTURE.md` (concepts), `docs/ARCHITECTURE_DIAGRAM.md` (visuals).

### Core Pieces
| File/Dir | Role |
|----------|------|
| `internal/commands/module_handler.go` | Registers modules & routes interactions |
| `internal/commands/types/` | Interfaces: `CommandModule`, `ModuleService`, `Dependencies` |
| `internal/commands/modules/` | One folder per feature (slash commands + helpers) |

### Module Pattern
Each module provides:
1. `New(deps *types.Dependencies)` – construct module with shared resources.
2. `Register(cmds map[string]*types.Command, deps *types.Dependencies)` – define slash command(s).
3. (Optional) `Service() types.ModuleService` – background lifecycle hooks.

### Services
| Module | Service Functionality |
|--------|-----------------------|
| `say` | Scheduled message dispatch |
| `roulette` | Pairing scheduling/execution engine |
| `welcome` | New member welcome workflow |

### Interaction Routing
UI interactions (modals/components) for complex modules (e.g. LFG) are forwarded directly to the module for cohesion. This keeps embedding/modal logic local to the feature.

## Deployment

Add `deploy-dev` label to a PR → CI deploys branch to dev bot automatically (no manual config). Production deploy uses same pipeline without the label.
If config changes required, coordinate via Discord before merging.

## Contributing

Generally, you can follow this approach:

1. Fork the repository
2. Create a feature branch
3. Make your changes
4. Submit a pull request

However, given the complexity of deploying the changes requiring a bot token, IGDB token, config file, etc. it's dramatically easier for you to contribute if you test your changes using the development bot instance by adding the `deploy-dev` labels to PRs. Adding this label will automatically deploy your branch to the developement bot instance without you needing to configure anything. If you don't have access to label PRs, please reach out in the GamerPals discord server at https://discord.gg/gamerpals. If you need to make manual config changes on either the production bot or developement bot, that's another reason to reach out in the Discord.

## License

Copyright (c) 2023 Kynan Ware all rights reserved.


## Support

For support, make a ticket in [GamerPals Discord server](https://discord.gg/gamerpals) or open an issue on GitHub.
