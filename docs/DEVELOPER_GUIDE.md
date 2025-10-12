# Developer Guide: Modular Commands

This guide provides practical instructions for working with the modular command architecture.

## Quick Start

### Understanding the Structure

```
internal/commands/
├── types/              # Shared interfaces (CommandModule, Dependencies, Command)
├── modules/           # All command modules
│   ├── ping/         # Each module is self-contained
│   ├── say/
│   └── ...
└── module_handler.go # Routes commands to modules
```

### Key Concepts

- **Module**: Self-contained unit containing command definition and logic
- **Dependencies**: Shared resources (Config, DB, IGDB Client, Session)
- **CommandModule Interface**: Contract all modules implement via `Register()`

## Common Tasks

### Adding a Simple Command

For commands without services or complex logic:

1. **Create the module directory**:
   ```bash
   mkdir -p internal/commands/modules/greet
   ```

2. **Create `greet.go`**:
   ```go
   package greet
   
   import (
       "gamerpal/internal/commands/types"
       "github.com/bwmarrin/discordgo"
   )
   
   type Module struct {
       config *config.Config
   }
   
   func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
       m.config = deps.Config
       
       cmds["greet"] = &types.Command{
           ApplicationCommand: &discordgo.ApplicationCommand{
               Name:        "greet",
               Description: "Send a friendly greeting",
           },
           HandlerFunc: m.handleGreet,
       }
   }
   
   func (m *Module) handleGreet(s *discordgo.Session, i *discordgo.InteractionCreate) {
       s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
           Type: discordgo.InteractionResponseChannelMessageWithSource,
           Data: &discordgo.InteractionResponseData{
               Content: "Hello! 👋",
           },
       })
   }
   ```

3. **Register in `module_handler.go`**:
   ```go
   import "gamerpal/internal/commands/modules/greet"
   
   func (h *ModuleHandler) registerModules() {
       // ... other modules ...
       
       greetModule := &greet.Module{}
       greetModule.Register(h.Commands, h.deps)
   }
   ```

### Adding a Command with Options

For commands that accept user input:

```go
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.config = deps.Config
    
    cmds["echo"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "echo",
            Description: "Echo back a message",
            Options: []*discordgo.ApplicationCommandOption{
                {
                    Type:        discordgo.ApplicationCommandOptionString,
                    Name:        "message",
                    Description: "Message to echo",
                    Required:    true,
                },
            },
        },
        HandlerFunc: m.handleEcho,
    }
}

func (m *Module) handleEcho(s *discordgo.Session, i *discordgo.InteractionCreate) {
    options := i.ApplicationCommandData().Options
    message := options[0].StringValue()
    
    s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
        Type: discordgo.InteractionResponseChannelMessageWithSource,
        Data: &discordgo.InteractionResponseData{
            Content: message,
        },
    })
}
```

### Adding a Command with Service

For commands that need background services:

1. **Create module directory with both files**:
   ```bash
   mkdir -p internal/commands/modules/reminder
   ```

2. **Create `service.go`**:
   ```go
   package reminder
   
   import (
       "gamerpal/internal/config"
       "time"
   )
   
   type Service struct {
       config *config.Config
       reminders map[string]time.Time
   }
   
   func NewService(cfg *config.Config) *Service {
       return &Service{
           config: cfg,
           reminders: make(map[string]time.Time),
       }
   }
   
   func (s *Service) AddReminder(userID string, when time.Time) {
       s.reminders[userID] = when
   }
   
   func (s *Service) CheckReminders() {
       // Called by scheduler
       now := time.Now()
       for userID, when := range s.reminders {
           if now.After(when) {
               // Send reminder
               delete(s.reminders, userID)
           }
       }
   }
   ```

3. **Create `reminder.go`**:
   ```go
   package reminder
   
   import (
       "gamerpal/internal/commands/types"
       "github.com/bwmarrin/discordgo"
   )
   
   type Module struct {
       config  *config.Config
       service *Service
   }
   
   func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
       m.config = deps.Config
       m.service = NewService(deps.Config)
       
       cmds["remind"] = &types.Command{
           ApplicationCommand: &discordgo.ApplicationCommand{
               Name:        "remind",
               Description: "Set a reminder",
               // ... options ...
           },
           HandlerFunc: m.handleRemind,
       }
   }
   
   func (m *Module) handleRemind(s *discordgo.Session, i *discordgo.InteractionCreate) {
       // Parse options and add reminder
       m.service.AddReminder(i.Member.User.ID, reminderTime)
       // Respond to user
   }
   
   // Expose service for scheduler
   func (m *Module) GetService() *Service {
       return m.service
   }
   ```

4. **Access from scheduler** (in `bot.go`):
   ```go
   reminderService := handler.GetReminderService()
   scheduler.RegisterNewMinuteFunc(func() error {
       reminderService.CheckReminders()
       return nil
   })
   ```

### Adding Multiple Related Commands

Group related commands in one module:

```go
func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    m.config = deps.Config
    
    // Main command
    cmds["tag"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "tag",
            Description: "Show a tag",
        },
        HandlerFunc: m.handleTagShow,
    }
    
    // Management command
    cmds["tag-admin"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "tag-admin",
            Description: "Manage tags",
        },
        HandlerFunc: m.handleTagAdmin,
    }
}
```

### Handling Modal Interactions

For commands that use modals:

```go
type Module struct {
    config *config.Config
}

func (m *Module) Register(cmds map[string]*types.Command, deps *types.Dependencies) {
    cmds["feedback"] = &types.Command{
        ApplicationCommand: &discordgo.ApplicationCommand{
            Name:        "feedback",
            Description: "Submit feedback",
        },
        HandlerFunc: m.handleFeedback,
    }
}

func (m *Module) handleFeedback(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Show modal
    s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
        Type: discordgo.InteractionResponseModal,
        Data: &discordgo.InteractionResponseData{
            CustomID: "feedback_modal",
            Title:    "Submit Feedback",
            Components: []discordgo.MessageComponent{
                discordgo.ActionsRow{
                    Components: []discordgo.MessageComponent{
                        discordgo.TextInput{
                            CustomID:  "feedback_text",
                            Label:     "Your Feedback",
                            Style:     discordgo.TextInputParagraph,
                            Required:  true,
                        },
                    },
                },
            },
        },
    })
}

// Handle modal submission
func (m *Module) HandleModalSubmit(s *discordgo.Session, i *discordgo.InteractionCreate) {
    data := i.ModalSubmitData()
    feedbackText := data.Components[0].(*discordgo.ActionsRow).Components[0].(*discordgo.TextInput).Value
    
    // Process feedback
    // ...
}
```

Register the modal handler in `module_handler.go`:
```go
case discordgo.InteractionModalSubmit:
    if i.ModalSubmitData().CustomID == "feedback_modal" {
        h.feedbackModule.HandleModalSubmit(s, i)
    }
```

## Testing

### Unit Testing a Module

```go
package mycommand

import (
    "testing"
    "gamerpal/internal/commands/types"
    "gamerpal/internal/config"
)

func TestModuleRegister(t *testing.T) {
    module := &Module{}
    commands := make(map[string]*types.Command)
    deps := &types.Dependencies{
        Config: config.NewTestConfig(),
    }
    
    module.Register(commands, deps)
    
    if commands["mycommand"] == nil {
        t.Fatal("Command not registered")
    }
    
    cmd := commands["mycommand"]
    if cmd.ApplicationCommand.Name != "mycommand" {
        t.Errorf("Expected name 'mycommand', got %s", cmd.ApplicationCommand.Name)
    }
}
```

### Integration Testing

```go
func TestCommandExecution(t *testing.T) {
    // Setup
    handler := NewModuleHandler(testConfig)
    session := createTestSession(t)
    interaction := createTestInteraction(t, "mycommand")
    
    // Execute
    handler.HandleInteraction(session, interaction)
    
    // Assert
    // Check that response was sent correctly
}
```

## Debugging

### Enable Development Mode

Mark a command as development-only:
```go
cmds["testcmd"] = &types.Command{
    ApplicationCommand: &discordgo.ApplicationCommand{
        Name:        "testcmd",
        Description: "Test command",
    },
    HandlerFunc: m.handleTest,
    Development: true,  // Won't register to Discord
}
```

### Logging

Use the config logger:
```go
func (m *Module) handleMyCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
    m.config.Logger.Infof("Handling mycommand from user %s", i.Member.User.ID)
    // ...
}
```

## Best Practices

1. **Keep modules focused**: One module = one feature/command group
2. **Use services for complexity**: Extract complex logic to `service.go`
3. **Handle errors gracefully**: Always respond to interactions, even on error
4. **Log appropriately**: Use Info for normal operations, Warn/Error for issues
5. **Test thoroughly**: Write tests for both happy and error paths
6. **Document non-obvious code**: Add comments for complex logic

## Common Patterns

### Deferred Response

For long-running operations:
```go
func (m *Module) handleSlow(s *discordgo.Session, i *discordgo.InteractionCreate) {
    // Acknowledge immediately
    s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
        Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
    })
    
    // Do slow work
    result := doSlowOperation()
    
    // Send actual response
    s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
        Content: &result,
    })
}
```

### Ephemeral Messages

For private responses:
```go
s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
    Type: discordgo.InteractionResponseChannelMessageWithSource,
    Data: &discordgo.InteractionResponseData{
        Content: "Only you can see this",
        Flags:   discordgo.MessageFlagsEphemeral,
    },
})
```

### Permission Checks

Restrict commands to admins:
```go
cmds["admin"] = &types.Command{
    ApplicationCommand: &discordgo.ApplicationCommand{
        Name:                     "admin",
        Description:              "Admin only command",
        DefaultMemberPermissions: &adminPerms,  // var adminPerms int64 = discordgo.PermissionAdministrator
    },
    HandlerFunc: m.handleAdmin,
}
```

## Troubleshooting

### Command not appearing in Discord
- Check that `Development` is not set to `true`
- Verify module is registered in `registerModules()`
- Discord can take up to 1 hour to sync global commands

### Handler not being called
- Verify command name matches exactly in both registration and Discord
- Check logs for routing errors
- Ensure `HandlerFunc` is set correctly

### Service not accessible
- Make sure you store module reference in handler
- Implement and expose `GetService()` method
- Verify service is initialized in `Register()`

## Additional Resources

- `ARCHITECTURE.md` - Architecture overview and principles
- `ARCHITECTURE_DIAGRAM.md` - Visual diagrams
- `modules/README.md` - Quick module reference
- Discord.go documentation: https://pkg.go.dev/github.com/bwmarrin/discordgo
