package lfg

import (
	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/utils"
	"slices"
	"sync"
	"time"
)

// LfgService handles scheduled tasks for the LFG module.
type LfgService struct {
	types.BaseService
	config    *config.Config
	activeNow sync.Map // userID → time.Time (when role was assigned)
}

// NewLfgService creates a new LFG service.
func NewLfgService(cfg *config.Config) *LfgService {
	return &LfgService{config: cfg}
}

// ScheduledFuncs returns scheduled tasks for the LFG module.
func (s *LfgService) ScheduledFuncs() map[string]func() error {
	return map[string]func() error{
		"@every 1m": func() error {
			s.reconcileLFGNowRole()
			return nil
		},
	}
}

// AssignLFGNowRole gives the user the LFG Now role and tracks the assignment.
// Returns the expiration time.
func (s *LfgService) AssignLFGNowRole(guildID, userID string) time.Time {
	roleID := s.config.GetLFGNowRoleID()
	if roleID == "" || s.Session == nil {
		return time.Time{}
	}

	_ = s.Session.GuildMemberRoleAdd(guildID, userID, roleID)
	expiresAt := time.Now().Add(s.config.GetLFGNowRoleDuration())
	s.activeNow.Store(userID, expiresAt)
	return expiresAt
}

// reconcileLFGNowRole removes the LFG Now role from users whose time has expired
// or who are not tracked in the map.
func (s *LfgService) reconcileLFGNowRole() {
	if s.Session == nil {
		return
	}
	roleID := s.config.GetLFGNowRoleID()
	guildID := s.config.GetGamerPalsServerID()
	if roleID == "" || guildID == "" {
		return
	}

	// Clean expired entries from the map
	now := time.Now()
	s.activeNow.Range(func(key, value any) bool {
		if expiresAt, ok := value.(time.Time); ok && now.After(expiresAt) {
			s.activeNow.Delete(key)
		}
		return true
	})

	// Get all guild members and remove the role from anyone not in the map
	members, err := utils.GetAllHumanGuildMembers(s.Session, guildID)
	if err != nil {
		s.config.Logger.Warnf("LFG: failed to fetch guild members for role reconciliation: %v", err)
		return
	}

	for _, member := range members {
		if !slices.Contains(member.Roles, roleID) {
			continue
		}
		if _, tracked := s.activeNow.Load(member.User.ID); !tracked {
			if err := s.Session.GuildMemberRoleRemove(guildID, member.User.ID, roleID); err != nil {
				s.config.Logger.Warnf("LFG: failed to remove LFG Now role from %s: %v", member.User.ID, err)
			}
		}
	}
}
