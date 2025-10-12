package roulette

import (
	"testing"

	"gamerpal/internal/commands/types"
	"gamerpal/internal/config"
	"gamerpal/internal/database"

	"github.com/stretchr/testify/require"
)

// TestModuleServiceSeparation verifies that the module and service are properly separated
func TestModuleServiceSeparation(t *testing.T) {
	// Create a test config
	cfg := &config.Config{}

	// Create a test database
	db, err := database.NewDB(":memory:")
	require.NoError(t, err)
	defer db.Close()

	deps := &types.Dependencies{
		Config: cfg,
		DB:     db,
	}

	// Create module
	module := New()
	require.NotNil(t, module)

	// Register commands
	commands := make(map[string]*types.Command)
	module.Register(commands, deps)

	// Get the service from the module
	service := module.Service()
	require.NotNil(t, service, "Service should not be nil")

	// Verify service implements ModuleService interface
	_, ok := service.(types.ModuleService)
	require.True(t, ok, "Service should implement ModuleService interface")

	// Verify the service is actually a PairingService
	pairingService, ok := service.(*PairingService)
	require.True(t, ok, "Service should be a PairingService")
	require.NotNil(t, pairingService)

	// Verify service has the expected methods from ModuleService interface
	require.NotNil(t, service.MinuteFuncs())
	require.Nil(t, service.HourFuncs())

	// Initialize service (this would normally be done by the module handler)
	err = service.InitializeService(nil)
	require.NoError(t, err)
}
