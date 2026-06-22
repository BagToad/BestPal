package agentengine

import (
	"strings"
	"testing"
)

func TestComposeStaticSystemPrompt(t *testing.T) {
	t.Run("returns base when internal is empty", func(t *testing.T) {
		got := composeStaticSystemPrompt("BASE", " \n ")
		if got != "BASE" {
			t.Fatalf("got %q, want BASE", got)
		}
	})

	t.Run("returns internal when base is empty", func(t *testing.T) {
		got := composeStaticSystemPrompt(" \n ", "INTERNAL")
		if got != "INTERNAL" {
			t.Fatalf("got %q, want INTERNAL", got)
		}
	})

	t.Run("appends internal below base", func(t *testing.T) {
		got := composeStaticSystemPrompt("BASE", "INTERNAL")
		if got != "BASE\n\nINTERNAL" {
			t.Fatalf("got %q, want BASE\\n\\nINTERNAL", got)
		}
	})
}

func TestInternalRequestModePromptContract(t *testing.T) {
	required := []string{
		"[[BESTPAL_INTERNAL_REQUEST]]",
		"single leading marker",
		"Find the game threads for the games <@userID> plays.",
		"\"game-threads\"",
		"\"name\": \"string\"",
		"\"url\": \"string\"",
		"\"status\": \"found | not found\"",
		"Only `url` is allowed to be empty.",
	}

	for _, s := range required {
		if !strings.Contains(internalRequestModePrompt, s) {
			t.Fatalf("internal prompt missing required content: %q", s)
		}
	}
}
