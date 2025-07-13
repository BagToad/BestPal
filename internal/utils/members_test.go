package utils

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestObfuscateID(t *testing.T) {
	tests := []struct {
		name    string
		userID  string
		salt    string
		wantErr bool
	}{
		{
			name:    "valid inputs",
			userID:  "123456789",
			salt:    "mysalt",
			wantErr: false,
		},
		{
			name:    "empty userID",
			userID:  "",
			salt:    "mysalt",
			wantErr: true,
		},
		{
			name:    "empty salt",
			userID:  "123456789",
			salt:    "",
			wantErr: true,
		},
		{
			name:    "both empty",
			userID:  "",
			salt:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := ObfuscateID(tt.userID, tt.salt)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			// Check format is "noun-adjective"
			parts := strings.Split(result, " ")
			require.Len(t, parts, 2, "Obfuscated ID should be in the format 'noun-adjective'")
		})
	}
}

func TestObfuscateID_DifferentInputs(t *testing.T) {
	salt := "testsalt"

	result1, _ := ObfuscateID("user1", salt)
	result2, _ := ObfuscateID("user2", salt)

	require.NotEqual(t, result1, result2, "Obfuscated IDs should be different for different user IDs")
}
