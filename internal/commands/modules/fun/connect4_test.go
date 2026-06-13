package fun

import (
	"strings"
	"sync"
	"testing"
)

// newTestGame returns an active game with an empty board and no Discord wiring,
// so updateGameMessage is a no-op (c4mgr.session is nil in tests).
func newTestGame() *Connect4Game {
	return &Connect4Game{
		ID:      "test",
		Player1: "p1",
		Player2: "p2",
		Turn:    c4Player1,
		Status:  c4Active,
	}
}

func TestC4DropPiece(t *testing.T) {
	g := newTestGame()

	// First drop in column 3 should land on the bottom row.
	row := c4DropPiece(g, 3, c4Player1)
	if row != c4Rows-1 {
		t.Fatalf("first drop landed on row %d, want %d", row, c4Rows-1)
	}

	// Second drop should stack on top of the first.
	row = c4DropPiece(g, 3, c4Player2)
	if row != c4Rows-2 {
		t.Fatalf("second drop landed on row %d, want %d", row, c4Rows-2)
	}

	// Fill the rest of the column; the next drop must report "full".
	for r := c4Rows - 3; r >= 0; r-- {
		if got := c4DropPiece(g, 3, c4Player1); got != r {
			t.Fatalf("fill drop landed on row %d, want %d", got, r)
		}
	}
	if got := c4DropPiece(g, 3, c4Player1); got != -1 {
		t.Fatalf("drop on full column returned %d, want -1", got)
	}
}

func TestC4CheckWinHorizontal(t *testing.T) {
	g := newTestGame()
	var lastRow, lastCol int
	for c := 0; c < 4; c++ {
		lastRow = c4DropPiece(g, c, c4Player1)
		lastCol = c
	}
	if !c4CheckWin(g, lastRow, lastCol, c4Player1) {
		t.Fatal("expected horizontal win")
	}
}

func TestC4CheckWinVertical(t *testing.T) {
	g := newTestGame()
	var lastRow int
	for i := 0; i < 4; i++ {
		lastRow = c4DropPiece(g, 2, c4Player2)
	}
	if !c4CheckWin(g, lastRow, 2, c4Player2) {
		t.Fatal("expected vertical win")
	}
}

func TestC4CheckWinDiagonalUp(t *testing.T) {
	// Build an ascending diagonal (/) for player 1.
	g := newTestGame()
	// Column heights needed for a diagonal at (5,0),(4,1),(3,2),(2,3).
	layout := [][]int{
		{c4Player1},                                  // col0: p1 at row5
		{c4Player2, c4Player1},                       // col1: p1 at row4
		{c4Player2, c4Player2, c4Player1},            // col2: p1 at row3
		{c4Player2, c4Player2, c4Player2, c4Player1}, // col3: p1 at row2
	}
	var lr, lc int
	for col, stack := range layout {
		for _, p := range stack {
			lr = c4DropPiece(g, col, p)
			lc = col
		}
	}
	if !c4CheckWin(g, lr, lc, c4Player1) {
		t.Fatal("expected ascending diagonal win")
	}
}

func TestC4CheckWinDiagonalDown(t *testing.T) {
	// Build a descending diagonal (\) for player 1 at (2,0),(3,1),(4,2),(5,3).
	g := newTestGame()
	layout := [][]int{
		{c4Player2, c4Player2, c4Player2, c4Player1}, // col0: p1 at row2
		{c4Player2, c4Player2, c4Player1},            // col1: p1 at row3
		{c4Player2, c4Player1},                       // col2: p1 at row4
		{c4Player1},                                  // col3: p1 at row5
	}
	var lr, lc int
	for col, stack := range layout {
		for _, p := range stack {
			lr = c4DropPiece(g, col, p)
			lc = col
		}
	}
	if !c4CheckWin(g, lr, lc, c4Player1) {
		t.Fatal("expected descending diagonal win")
	}
}

func TestC4CheckWinNegative(t *testing.T) {
	g := newTestGame()
	// Three in a row is not a win.
	for c := 0; c < 3; c++ {
		c4DropPiece(g, c, c4Player1)
	}
	if c4CheckWin(g, c4Rows-1, 2, c4Player1) {
		t.Fatal("three in a row should not win")
	}
}

func TestC4CheckDraw(t *testing.T) {
	g := newTestGame()
	if c4CheckDraw(g) {
		t.Fatal("empty board is not a draw")
	}
	// Fill the board completely.
	for col := 0; col < c4Cols; col++ {
		for row := 0; row < c4Rows; row++ {
			g.Board[row][col] = c4Player1
		}
	}
	if !c4CheckDraw(g) {
		t.Fatal("full board should be a draw")
	}
}

func TestC4MoveTurnEnforcement(t *testing.T) {
	g := newTestGame()
	// Player 2 cannot move on player 1's turn.
	c4HandleReactionMove(nil, g, "p2", 0)
	if g.Board[c4Rows-1][0] != c4Empty {
		t.Fatal("out-of-turn move should be ignored")
	}
	// Player 1 moves; turn passes to player 2.
	c4HandleReactionMove(nil, g, "p1", 0)
	if g.Board[c4Rows-1][0] != c4Player1 {
		t.Fatal("player 1 move did not register")
	}
	if g.Turn != c4Player2 {
		t.Fatalf("turn did not pass to player 2, got %d", g.Turn)
	}
}

func TestC4MoveNonPlayerIgnored(t *testing.T) {
	g := newTestGame()
	c4HandleReactionMove(nil, g, "stranger", 0)
	if g.Board[c4Rows-1][0] != c4Empty {
		t.Fatal("non-player move should be ignored")
	}
}

func TestC4MoveWinFinishesGame(t *testing.T) {
	g := newTestGame()
	// p1 and p2 alternate; p1 builds a vertical four in column 0.
	moves := []struct {
		user string
		col  int
	}{
		{"p1", 0}, {"p2", 1},
		{"p1", 0}, {"p2", 1},
		{"p1", 0}, {"p2", 1},
		{"p1", 0}, // winning move
	}
	for _, m := range moves {
		c4HandleReactionMove(nil, g, m.user, m.col)
	}
	if g.Status != c4Finished {
		t.Fatalf("game should be finished after a win, status=%d", g.Status)
	}
	if g.Winner != c4Player1 {
		t.Fatalf("winner should be player 1, got %d", g.Winner)
	}
}

// TestC4ConcurrentMoves hammers a game from multiple goroutines to surface data
// races under `go test -race`. Both players spam every column at once.
func TestC4ConcurrentMoves(t *testing.T) {
	g := newTestGame()
	var wg sync.WaitGroup
	for iter := 0; iter < 50; iter++ {
		for _, user := range []string{"p1", "p2"} {
			for col := 0; col < c4Cols; col++ {
				wg.Add(1)
				go func(u string, c int) {
					defer wg.Done()
					c4HandleReactionMove(nil, g, u, c)
				}(user, col)
			}
		}
	}
	wg.Wait()
}

func TestC4FinishedEmbedHidesEmptyBoard(t *testing.T) {
	// A declined challenge: finished, no moves played -> no board grid.
	g := newTestGame()
	g.Status = c4Finished
	g.Moves = 0
	g.WinReason = "❌ <@p1> cancelled the challenge."
	if got := c4BuildGameEmbed(g).Description; strings.Contains(got, c4EmojiEmpty) {
		t.Fatalf("declined game should not render an empty board, got:\n%s", got)
	}

	// A played-out game should still render the board.
	g2 := newTestGame()
	g2.Status = c4Finished
	g2.Moves = 1
	g2.Board[c4Rows-1][0] = c4Player1
	if got := c4BuildGameEmbed(g2).Description; !strings.Contains(got, c4EmojiEmpty) {
		t.Fatalf("played game should render the board, got:\n%s", got)
	}
}

func TestC4NormalizeEmoji(t *testing.T) {
	// Flag with and without the variation selector must compare equal.
	withVS := "\U0001F3F3\uFE0F"
	withoutVS := "\U0001F3F3"
	if c4NormalizeEmoji(withVS) != c4NormalizeEmoji(withoutVS) {
		t.Fatal("forfeit emoji should match regardless of variation selector")
	}
	// Keycaps normalize consistently too.
	if c4NormalizeEmoji("1\uFE0F\u20E3") != c4NormalizeEmoji("1\u20E3") {
		t.Fatal("keycap emoji should match regardless of variation selector")
	}
}

func TestC4Other(t *testing.T) {
	if c4Other(c4Player1) != c4Player2 || c4Other(c4Player2) != c4Player1 {
		t.Fatal("c4Other should return the opposing player")
	}
}

func TestC4RouteReactionIntentColumnMove(t *testing.T) {
	g := newTestGame()
	c4RouteReactionIntent(nil, g, "p1", c4ColumnEmoji[0])
	if g.Board[c4Rows-1][0] != c4Player1 {
		t.Fatal("a column reaction should drop a piece for the player")
	}
}

func TestC4RouteReactionIntentForfeit(t *testing.T) {
	g := newTestGame()
	c4RouteReactionIntent(nil, g, "p1", c4EmojiForfeit)
	if g.Status != c4Finished || g.Winner != c4Player2 {
		t.Fatalf("forfeit reaction should finish the game for the opponent, status=%d winner=%d", g.Status, g.Winner)
	}
}

// TestC4SelfRemovalSuppression locks in the bookkeeping that lets the remove
// handler tell its own cleanup removal apart from a player toggling a reaction
// off: an unknown removal is real input, a marked one is swallowed exactly once.
func TestC4SelfRemovalSuppression(t *testing.T) {
	msg, user, emoji := "msg-suppress", "userA", c4ColumnEmoji[2]

	if c4mgr.consumeSelfRemoval(msg, user, emoji) {
		t.Fatal("a removal with no pending token should not be suppressed")
	}

	c4mgr.markSelfRemoval(msg, user, emoji)
	if !c4mgr.consumeSelfRemoval(msg, user, emoji) {
		t.Fatal("the bot's own removal should be suppressed once")
	}
	if c4mgr.consumeSelfRemoval(msg, user, emoji) {
		t.Fatal("a second removal should no longer be suppressed")
	}
}

func TestC4SelfRemovalUnmark(t *testing.T) {
	msg, user, emoji := "msg-unmark", "userB", c4ColumnEmoji[3]
	c4mgr.markSelfRemoval(msg, user, emoji)
	c4mgr.unmarkSelfRemoval(msg, user, emoji)
	if c4mgr.consumeSelfRemoval(msg, user, emoji) {
		t.Fatal("an unmarked token should not suppress a later removal")
	}
}

func TestC4RemovalKeyNormalizesVariationSelector(t *testing.T) {
	withVS := c4RemovalKey("m", "u", "\U0001F3F3\uFE0F")
	withoutVS := c4RemovalKey("m", "u", "\U0001F3F3")
	if withVS != withoutVS {
		t.Fatal("removal key should match regardless of variation selector")
	}
}

func TestC4PurgePendingRemovals(t *testing.T) {
	msg := "msg-purge"
	c4mgr.markSelfRemoval(msg, "u1", c4ColumnEmoji[0])
	c4mgr.markSelfRemoval(msg, "u2", c4ColumnEmoji[1])

	c4mgr.mu.Lock()
	c4mgr.purgePendingRemovalsLocked(msg)
	c4mgr.mu.Unlock()

	if c4mgr.consumeSelfRemoval(msg, "u1", c4ColumnEmoji[0]) || c4mgr.consumeSelfRemoval(msg, "u2", c4ColumnEmoji[1]) {
		t.Fatal("purged tokens should no longer suppress removals")
	}
}

// TestC4SelfRemovalConcurrent exercises the token map from many goroutines so
// `go test -race` can catch unsynchronized access.
func TestC4SelfRemovalConcurrent(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			emoji := c4ColumnEmoji[n%c4Cols]
			c4mgr.markSelfRemoval("cmsg", "cuser", emoji)
			c4mgr.consumeSelfRemoval("cmsg", "cuser", emoji)
		}(i)
	}
	wg.Wait()
}

func TestC4MoveDrawFinishesGame(t *testing.T) {
	g := newTestGame()
	// Fill the whole board, then empty the top-left cell so exactly one legal
	// move remains. Surround that cell so the final piece completes no line:
	// the draw path is what's under test, not board realism.
	for col := 0; col < c4Cols; col++ {
		for row := 0; row < c4Rows; row++ {
			g.Board[row][col] = c4Player2
		}
	}
	g.Board[0][0] = c4Empty // the only open slot, lands at row 0
	g.Moves = c4Rows*c4Cols - 1
	g.Turn = c4Player1

	c4HandleReactionMove(nil, g, "p1", 0)

	if g.Board[0][0] != c4Player1 {
		t.Fatal("final move did not register")
	}
	if g.Status != c4Finished {
		t.Fatalf("full board should finish the game, status=%d", g.Status)
	}
	if g.Winner != 0 {
		t.Fatalf("a filled board with no completed line should be a draw, winner=%d", g.Winner)
	}
}
