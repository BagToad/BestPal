package fun

import (
	"math/rand"

	copilot "github.com/github/copilot-sdk/go"
)

// coinflipFaces are the two sides of the coin, heads first.
var coinflipFaces = [2]string{"🫃", "🫄"}

// coinflipIntn picks a face; swapped out in tests.
var coinflipIntn = rand.Intn

type coinflipResult struct {
	Result string `json:"result"`
}

// AgentTools satisfies the duck-typed agentToolProvider in the commands package.
func (m *Module) AgentTools() []copilot.Tool {
	return []copilot.Tool{newCoinflipTool()}
}

func newCoinflipTool() copilot.Tool {
	type empty struct{}
	t := copilot.DefineTool(
		"coinflip",
		`Flip a coin. Use when someone asks you to flip a coin, call heads or tails, or make a random 50/50 pick. Takes no arguments. The result is either 🫃 or 🫄; report it exactly as returned and do not pick it yourself.`,
		func(_ empty, _ copilot.ToolInvocation) (*coinflipResult, error) {
			return &coinflipResult{Result: coinflipFaces[coinflipIntn(len(coinflipFaces))]}, nil
		},
	)
	t.SkipPermission = true
	return t
}
