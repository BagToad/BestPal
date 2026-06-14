package fun

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"gamerpal/internal/config"

	"github.com/bwmarrin/discordgo"
)

// --- Constants ---

const (
	c4Rows = 6
	c4Cols = 7

	c4Empty   = 0
	c4Player1 = 1
	c4Player2 = 2

	c4Timeout = 5 * time.Minute

	c4EmojiEmpty   = "⚫"
	c4EmojiPlayer1 = "🔴"
	c4EmojiPlayer2 = "🟡"
	c4EmojiForfeit = "🏳️"

	// Cursed mode swaps the piece emoji for these.
	c4EmojiCursedPlayer1 = "🫃"
	c4EmojiCursedPlayer2 = "🫄"

	c4ModeNormal = "normal"
	c4ModeCursed = "cursed"

	c4ColorRed    = 0xE74C3C
	c4ColorYellow = 0xF1C40F
	c4ColorGreen  = 0x2ECC71
	c4ColorGray   = 0x95A5A6
	c4ColorBlue   = 0x3498DB
)

// c4Theme holds the piece emoji used to render each player for a game mode.
type c4Theme struct {
	Player1 string
	Player2 string
}

var c4Themes = map[string]c4Theme{
	c4ModeNormal: {Player1: c4EmojiPlayer1, Player2: c4EmojiPlayer2},
	c4ModeCursed: {Player1: c4EmojiCursedPlayer1, Player2: c4EmojiCursedPlayer2},
}

// Column reaction emoji - users react with these to pick a column.
var c4ColumnEmoji = []string{"1️⃣", "2️⃣", "3️⃣", "4️⃣", "5️⃣", "6️⃣", "7️⃣"}

type c4Status int

const (
	c4Pending c4Status = iota
	c4Active
	c4Finished
)

// --- Game State ---

type Connect4Game struct {
	ID           string
	Board        [c4Rows][c4Cols]int
	Player1      string // challenger (🔴)
	Player2      string // challenged (🟡)
	Mode         string // c4ModeNormal or c4ModeCursed
	Turn         int    // c4Player1 or c4Player2
	ChannelID    string
	MessageID    string // the game board message
	Status       c4Status
	Winner       int // 0 = draw, c4Player1 or c4Player2
	WinReason    string
	Moves        int  // pieces dropped so far; used to decide whether to render the board
	Accepted     bool // true once the challenge is accepted and play reactions are added
	LastActivity time.Time
}

// theme resolves the piece emoji set for the game's mode, falling back to
// normal for an empty or unknown mode.
func (g *Connect4Game) theme() c4Theme {
	if t, ok := c4Themes[g.Mode]; ok {
		return t
	}
	return c4Themes[c4ModeNormal]
}

// pieceEmoji returns the emoji for a board cell value in this game's theme.
func (g *Connect4Game) pieceEmoji(player int) string {
	t := g.theme()
	switch player {
	case c4Player1:
		return t.Player1
	case c4Player2:
		return t.Player2
	default:
		return c4EmojiEmpty
	}
}

// --- Manager ---

type connect4Manager struct {
	games     map[string]*Connect4Game
	msgToGame map[string]string // messageID → gameID for fast reaction lookup
	// pendingRemovals counts reaction removals the bot initiated itself (while
	// cleaning up after a move). Keyed by messageID|userID|emoji so the matching
	// gateway remove event can be swallowed instead of replayed as a move.
	pendingRemovals map[string]int
	mu              sync.Mutex
	session         *discordgo.Session
	cfg             *config.Config
	once            sync.Once
}

type c4ExpiredGame struct {
	game     *Connect4Game
	accepted bool
}

var c4mgr = &connect4Manager{
	games:           make(map[string]*Connect4Game),
	msgToGame:       make(map[string]string),
	pendingRemovals: make(map[string]int),
}

func (mgr *connect4Manager) init(s *discordgo.Session, cfg *config.Config) {
	mgr.once.Do(func() {
		mgr.session = s
		mgr.cfg = cfg
		go mgr.cleanupLoop()
	})
}

func (mgr *connect4Manager) createChallenge(player1, player2, channelID, mode string) *Connect4Game {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	game := &Connect4Game{
		ID:           generateGameID(),
		Player1:      player1,
		Player2:      player2,
		Mode:         mode,
		Turn:         c4Player1,
		ChannelID:    channelID,
		Status:       c4Pending,
		LastActivity: time.Now(),
	}
	mgr.games[game.ID] = game
	return game
}

// linkMessage associates a message ID with a game for reaction lookups.
func (mgr *connect4Manager) linkMessage(messageID, gameID string) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	mgr.msgToGame[messageID] = gameID
}

func (mgr *connect4Manager) getGame(id string) *Connect4Game {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.games[id]
}

func (mgr *connect4Manager) getGameByMessage(messageID string) *Connect4Game {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	gameID, ok := mgr.msgToGame[messageID]
	if !ok {
		return nil
	}
	return mgr.games[gameID]
}

func (mgr *connect4Manager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		expired, session := mgr.collectExpired()
		for _, e := range expired {
			mgr.updateGameMessage(e.game)
			if e.accepted {
				c4ClearReactions(session, e.game)
			}
		}
		mgr.removeStale()
	}
}

func (mgr *connect4Manager) collectExpired() ([]c4ExpiredGame, *discordgo.Session) {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	var expired []c4ExpiredGame
	now := time.Now()
	session := mgr.session

	for _, game := range mgr.games {
		if game.Status == c4Finished || now.Sub(game.LastActivity) <= c4Timeout {
			continue
		}
		prevStatus := game.Status
		game.Status = c4Finished
		game.LastActivity = now

		if prevStatus == c4Pending {
			game.WinReason = "⏰ Challenge expired."
		} else {
			if game.Turn == c4Player1 {
				game.Winner = c4Player2
				game.WinReason = fmt.Sprintf("⏰ <@%s> took too long - %s <@%s> wins!", game.Player1, game.theme().Player2, game.Player2)
			} else {
				game.Winner = c4Player1
				game.WinReason = fmt.Sprintf("⏰ <@%s> took too long - %s <@%s> wins!", game.Player2, game.theme().Player1, game.Player1)
			}
		}
		expired = append(expired, c4ExpiredGame{
			game:     game,
			accepted: game.Accepted,
		})
	}
	return expired, session
}

func (mgr *connect4Manager) removeStale() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	now := time.Now()
	for id, game := range mgr.games {
		if game.Status == c4Finished && now.Sub(game.LastActivity) > 5*time.Minute {
			mgr.purgePendingRemovalsLocked(game.MessageID)
			delete(mgr.msgToGame, game.MessageID)
			delete(mgr.games, id)
		}
	}
}

// --- Self-removal bookkeeping ---
//
// The bot removes a player's reaction after each move to keep the row tidy.
// That removal fires a gateway remove event indistinguishable from a player
// toggling their own reaction off. We record each bot-initiated removal so the
// remove handler can swallow exactly that event and still treat genuine player
// toggles as moves.

func c4RemovalKey(messageID, userID, emojiAPIName string) string {
	return messageID + "|" + userID + "|" + c4NormalizeEmoji(emojiAPIName)
}

func (mgr *connect4Manager) markSelfRemoval(messageID, userID, emojiAPIName string) {
	key := c4RemovalKey(messageID, userID, emojiAPIName)
	mgr.mu.Lock()
	mgr.pendingRemovals[key]++
	mgr.mu.Unlock()
}

func (mgr *connect4Manager) unmarkSelfRemoval(messageID, userID, emojiAPIName string) {
	key := c4RemovalKey(messageID, userID, emojiAPIName)
	mgr.mu.Lock()
	mgr.decPendingLocked(key)
	mgr.mu.Unlock()
}

// consumeSelfRemoval reports whether the given removal was one the bot itself
// performed, decrementing the pending count when so.
func (mgr *connect4Manager) consumeSelfRemoval(messageID, userID, emojiAPIName string) bool {
	key := c4RemovalKey(messageID, userID, emojiAPIName)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if mgr.pendingRemovals[key] > 0 {
		mgr.decPendingLocked(key)
		return true
	}
	return false
}

func (mgr *connect4Manager) decPendingLocked(key string) {
	if mgr.pendingRemovals[key] > 0 {
		mgr.pendingRemovals[key]--
		if mgr.pendingRemovals[key] == 0 {
			delete(mgr.pendingRemovals, key)
		}
	}
}

// purgePendingRemovalsLocked drops any outstanding self-removal tokens for a
// message. Callers must hold mgr.mu.
func (mgr *connect4Manager) purgePendingRemovalsLocked(messageID string) {
	prefix := messageID + "|"
	for key := range mgr.pendingRemovals {
		if strings.HasPrefix(key, prefix) {
			delete(mgr.pendingRemovals, key)
		}
	}
}

// cleanupUserReaction removes a player's reaction to keep the row tidy, marking
// the removal first so its gateway event isn't replayed as a move. If the API
// call fails (commonly missing Manage Messages) no event will arrive, so the
// token is dropped to avoid swallowing the player's next genuine toggle.
func (mgr *connect4Manager) cleanupUserReaction(s *discordgo.Session, channelID, messageID, userID, emojiAPIName string) {
	mgr.markSelfRemoval(messageID, userID, emojiAPIName)
	if err := s.MessageReactionRemove(channelID, messageID, emojiAPIName, userID); err != nil {
		mgr.unmarkSelfRemoval(messageID, userID, emojiAPIName)
	}
}

// updateGameMessage edits the game's Discord message with the current state.
// The embed and components are built while holding the lock so the rendered
// board can't tear against a concurrent move; the network call runs unlocked.
func (mgr *connect4Manager) updateGameMessage(game *Connect4Game) {
	mgr.mu.Lock()
	if mgr.session == nil || game.MessageID == "" {
		mgr.mu.Unlock()
		return
	}
	session := mgr.session
	channelID := game.ChannelID
	messageID := game.MessageID
	embed := c4BuildGameEmbed(game)
	components := c4BuildComponents(game)
	mgr.mu.Unlock()

	_, _ = session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    channelID,
		ID:         messageID,
		Embeds:     &[]*discordgo.MessageEmbed{embed},
		Components: &components,
	})
}

// --- Game Logic ---

func c4DropPiece(game *Connect4Game, col, player int) int {
	for row := c4Rows - 1; row >= 0; row-- {
		if game.Board[row][col] == c4Empty {
			game.Board[row][col] = player
			return row
		}
	}
	return -1
}

func c4CheckWin(game *Connect4Game, row, col, player int) bool {
	directions := [][2]int{{0, 1}, {1, 0}, {1, 1}, {1, -1}}
	for _, dir := range directions {
		count := 1
		for i := 1; i < 4; i++ {
			r, c := row+dir[0]*i, col+dir[1]*i
			if r < 0 || r >= c4Rows || c < 0 || c >= c4Cols || game.Board[r][c] != player {
				break
			}
			count++
		}
		for i := 1; i < 4; i++ {
			r, c := row-dir[0]*i, col-dir[1]*i
			if r < 0 || r >= c4Rows || c < 0 || c >= c4Cols || game.Board[r][c] != player {
				break
			}
			count++
		}
		if count >= 4 {
			return true
		}
	}
	return false
}

func c4CheckDraw(game *Connect4Game) bool {
	for col := range c4Cols {
		if game.Board[0][col] == c4Empty {
			return false
		}
	}
	return true
}

// --- Rendering ---

func c4RenderBoard(game *Connect4Game) string {
	var sb strings.Builder
	for row := range c4Rows {
		for col := range c4Cols {
			sb.WriteString(game.pieceEmoji(game.Board[row][col]))
		}
		sb.WriteString("\n")
	}
	// Column labels below the board
	sb.WriteString(strings.Join(c4ColumnEmoji, ""))
	return sb.String()
}

func c4BuildGameEmbed(game *Connect4Game) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Game %s • React with 1️⃣-7️⃣ to play, 🏳️ to forfeit", game.ID)},
	}
	var desc strings.Builder
	theme := game.theme()

	switch game.Status {
	case c4Pending:
		embed.Title = "⚔️ Connect 4 Challenge"
		embed.Footer.Text = fmt.Sprintf("Game %s", game.ID)
		desc.WriteString(fmt.Sprintf("%s <@%s> has challenged %s <@%s> to a game of Connect 4!\n\n", theme.Player1, game.Player1, theme.Player2, game.Player2))
		desc.WriteString(fmt.Sprintf("Waiting for <@%s> to respond...", game.Player2))
		embed.Color = c4ColorBlue

	case c4Active:
		embed.Title = "🎮 Connect 4"
		desc.WriteString(fmt.Sprintf("%s <@%s>  vs  %s <@%s>\n\n", theme.Player1, game.Player1, theme.Player2, game.Player2))
		desc.WriteString(c4RenderBoard(game))
		if game.Turn == c4Player1 {
			desc.WriteString(fmt.Sprintf("\n%s <@%s>'s turn", theme.Player1, game.Player1))
			embed.Color = c4ColorRed
		} else {
			desc.WriteString(fmt.Sprintf("\n%s <@%s>'s turn", theme.Player2, game.Player2))
			embed.Color = c4ColorYellow
		}

	case c4Finished:
		embed.Title = "🎮 Connect 4"
		embed.Footer.Text = fmt.Sprintf("Game %s • Game over", game.ID)
		desc.WriteString(fmt.Sprintf("%s <@%s>  vs  %s <@%s>\n\n", theme.Player1, game.Player1, theme.Player2, game.Player2))
		// Only show the board if at least one piece was played; declined,
		// cancelled, and pre-start timeouts shouldn't render an empty grid.
		if game.Moves > 0 {
			desc.WriteString(c4RenderBoard(game))
			desc.WriteString("\n")
		}
		if game.WinReason != "" {
			desc.WriteString(game.WinReason)
		} else if game.Winner == c4Player1 {
			desc.WriteString(fmt.Sprintf("🎉 %s <@%s> wins!", theme.Player1, game.Player1))
		} else if game.Winner == c4Player2 {
			desc.WriteString(fmt.Sprintf("🎉 %s <@%s> wins!", theme.Player2, game.Player2))
		} else {
			desc.WriteString("🤝 It's a draw!")
		}
		if game.Winner != 0 {
			embed.Color = c4ColorGreen
		} else {
			embed.Color = c4ColorGray
		}
	}

	embed.Description = desc.String()
	return embed
}

// c4BuildComponents returns Accept/Decline buttons for pending games, empty otherwise.
// Active games use reactions instead of buttons for moves.
func c4BuildComponents(game *Connect4Game) []discordgo.MessageComponent {
	if game.Status != c4Pending {
		return []discordgo.MessageComponent{}
	}
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Accept",
					Style:    discordgo.SuccessButton,
					CustomID: fmt.Sprintf("c4:a:%s", game.ID),
					Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
				},
				discordgo.Button{
					Label:    "Decline",
					Style:    discordgo.DangerButton,
					CustomID: fmt.Sprintf("c4:d:%s", game.ID),
					Emoji:    &discordgo.ComponentEmoji{Name: "❌"},
				},
			},
		},
	}
}

// c4AddReactions adds column emoji + forfeit reactions to the game message.
func c4AddReactions(s *discordgo.Session, channelID, messageID string) {
	for _, emoji := range c4ColumnEmoji {
		_ = s.MessageReactionAdd(channelID, messageID, emoji)
	}
	_ = s.MessageReactionAdd(channelID, messageID, c4EmojiForfeit)
}

// --- Slash Command Handler ---

func (m *Module) handleConnect4Challenge(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c4mgr.init(s, m.config)

	var opponent *discordgo.User
	mode := c4ModeNormal
	for _, opt := range i.ApplicationCommandData().Options {
		switch opt.Name {
		case "opponent":
			opponent = opt.UserValue(s)
		case "mode":
			mode = opt.StringValue()
		}
	}

	if opponent == nil {
		c4RespondEphemeral(s, i, "❌ Could not find that user.")
		return
	}

	challenger := i.Member.User
	if opponent.ID == challenger.ID {
		c4RespondEphemeral(s, i, "❌ You can't challenge yourself!")
		return
	}
	if opponent.Bot {
		c4RespondEphemeral(s, i, "❌ You can't challenge a bot!")
		return
	}

	game := c4mgr.createChallenge(challenger.ID, opponent.ID, i.ChannelID, mode)
	embed := c4BuildGameEmbed(game)
	components := c4BuildComponents(game)

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content:    fmt.Sprintf("<@%s>, you've been challenged!", opponent.ID),
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
	if err != nil {
		return
	}

	msg, err := s.InteractionResponse(i.Interaction)
	if err == nil {
		c4mgr.mu.Lock()
		game.MessageID = msg.ID
		c4mgr.mu.Unlock()
		c4mgr.linkMessage(msg.ID, game.ID)
	}
}

// --- Component Handlers (Accept / Decline only) ---

func (m *Module) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c4mgr.init(s, m.config)

	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, ":", 4)
	if len(parts) < 3 {
		return
	}

	gameID := parts[2]

	switch parts[1] {
	case "a":
		m.handleC4Accept(s, i, gameID)
	case "d":
		m.handleC4Decline(s, i, gameID)
	}
}

func (m *Module) handleC4Accept(s *discordgo.Session, i *discordgo.InteractionCreate, gameID string) {
	game := c4mgr.getGame(gameID)
	if game == nil {
		c4RespondEphemeral(s, i, "❌ This game no longer exists.")
		return
	}

	userID := c4GetUserID(i)
	if userID != game.Player2 {
		c4RespondEphemeral(s, i, "❌ Only the challenged player can accept.")
		return
	}

	c4mgr.mu.Lock()
	if game.Status != c4Pending {
		c4mgr.mu.Unlock()
		c4RespondEphemeral(s, i, "❌ This challenge has already been responded to.")
		return
	}
	game.Status = c4Active
	game.Accepted = true
	game.LastActivity = time.Now()
	embed := c4BuildGameEmbed(game)
	c4mgr.mu.Unlock()

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "",
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{}, // remove accept/decline buttons
		},
	})

	// Add column reactions for gameplay
	go c4AddReactions(s, game.ChannelID, game.MessageID)
}

func (m *Module) handleC4Decline(s *discordgo.Session, i *discordgo.InteractionCreate, gameID string) {
	game := c4mgr.getGame(gameID)
	if game == nil {
		c4RespondEphemeral(s, i, "❌ This game no longer exists.")
		return
	}

	userID := c4GetUserID(i)
	if userID != game.Player1 && userID != game.Player2 {
		c4RespondEphemeral(s, i, "❌ Only the players can respond to this challenge.")
		return
	}

	c4mgr.mu.Lock()
	if game.Status != c4Pending {
		c4mgr.mu.Unlock()
		c4RespondEphemeral(s, i, "❌ This challenge has already been responded to.")
		return
	}
	game.Status = c4Finished
	game.LastActivity = time.Now()
	if userID == game.Player1 {
		game.WinReason = fmt.Sprintf("❌ <@%s> cancelled the challenge.", game.Player1)
	} else {
		game.WinReason = fmt.Sprintf("❌ <@%s> declined the challenge.", game.Player2)
	}
	embed := c4BuildGameEmbed(game)
	c4mgr.mu.Unlock()

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "",
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{},
		},
	})
}

// --- Reaction Handler ---

// IsConnect4Message returns true if the given message ID belongs to an active Connect 4 game.
func IsConnect4Message(messageID string) bool {
	c4mgr.mu.Lock()
	defer c4mgr.mu.Unlock()
	_, ok := c4mgr.msgToGame[messageID]
	return ok
}

// HandleReactionAdd processes a reaction added on a Connect 4 game message.
// Called from the bot's reaction event handler.
func HandleReactionAdd(s *discordgo.Session, r *discordgo.MessageReactionAdd) {
	// Ignore bot's own reactions
	if r.UserID == s.State.User.ID {
		return
	}

	game := c4mgr.getGameByMessage(r.MessageID)
	if game == nil {
		return
	}

	// Keep the board clean by removing the user's reaction once handled. Record
	// the removal first so the resulting gateway event is recognized as our own
	// and not replayed as a phantom move (see HandleReactionRemove).
	defer c4mgr.cleanupUserReaction(s, r.ChannelID, r.MessageID, r.UserID, r.Emoji.APIName())

	c4RouteReactionIntent(s, game, r.UserID, r.Emoji.Name)
}

// HandleReactionRemove processes a reaction removed from a Connect 4 game
// message. A removal counts as input too: when the bot's cleanup is lagging
// (Discord rate-limits reaction deletes) a player's reaction lingers, so their
// next tap on that column toggles it off, and that tap should still register.
// Removals the bot performed itself are swallowed so they don't replay as moves.
func HandleReactionRemove(s *discordgo.Session, r *discordgo.MessageReactionRemove) {
	if r.UserID == s.State.User.ID {
		return
	}

	game := c4mgr.getGameByMessage(r.MessageID)
	if game == nil {
		return
	}

	// Swallow the removal the bot itself issued while cleaning up after a move.
	if c4mgr.consumeSelfRemoval(r.MessageID, r.UserID, r.Emoji.APIName()) {
		return
	}

	c4RouteReactionIntent(s, game, r.UserID, r.Emoji.Name)
}

// c4RouteReactionIntent interprets a reaction emoji as a forfeit or column move
// for an active game. Shared by the add and remove handlers so either toggle
// direction registers as the same input.
func c4RouteReactionIntent(s *discordgo.Session, game *Connect4Game, userID, emojiName string) {
	// Normalize away variation selectors: Discord doesn't reliably include
	// U+FE0F on reaction payloads, which otherwise breaks emoji matching.
	name := c4NormalizeEmoji(emojiName)

	if name == c4NormalizeEmoji(c4EmojiForfeit) {
		c4HandleReactionForfeit(s, game, userID)
		return
	}

	for i, emoji := range c4ColumnEmoji {
		if name == c4NormalizeEmoji(emoji) {
			c4HandleReactionMove(s, game, userID, i)
			return
		}
	}
}

func c4HandleReactionMove(s *discordgo.Session, game *Connect4Game, userID string, col int) {
	player := c4PlayerForUser(game, userID)
	if player == c4Empty {
		return // not a player
	}

	c4mgr.mu.Lock()

	if game.Status != c4Active || game.Turn != player {
		c4mgr.mu.Unlock()
		return
	}

	row := c4DropPiece(game, col, player)
	if row == -1 {
		c4mgr.mu.Unlock()
		return // column full, just ignore
	}

	game.Moves++
	game.LastActivity = time.Now()

	finished := true
	switch {
	case c4CheckWin(game, row, col, player):
		game.Status = c4Finished
		game.Winner = player
	case c4CheckDraw(game):
		game.Status = c4Finished
		game.Winner = 0
	default:
		game.Turn = c4Other(player)
		finished = false
	}

	c4mgr.mu.Unlock()

	c4mgr.updateGameMessage(game)
	if finished {
		c4ClearReactions(s, game)
	}
}

func c4HandleReactionForfeit(s *discordgo.Session, game *Connect4Game, userID string) {
	c4mgr.mu.Lock()

	if game.Status != c4Active {
		c4mgr.mu.Unlock()
		return
	}

	switch userID {
	case game.Player1:
		game.Winner = c4Player2
		game.WinReason = fmt.Sprintf("%s <@%s> forfeited - %s <@%s> wins!", c4EmojiForfeit, game.Player1, game.theme().Player2, game.Player2)
	case game.Player2:
		game.Winner = c4Player1
		game.WinReason = fmt.Sprintf("%s <@%s> forfeited - %s <@%s> wins!", c4EmojiForfeit, game.Player2, game.theme().Player1, game.Player1)
	default:
		c4mgr.mu.Unlock()
		return
	}

	game.Status = c4Finished
	game.LastActivity = time.Now()
	c4mgr.mu.Unlock()

	c4mgr.updateGameMessage(game)
	c4ClearReactions(s, game)
}

// --- Helpers ---

// c4PlayerForUser maps a Discord user ID to its player number, or c4Empty if
// the user isn't a participant in the game.
func c4PlayerForUser(game *Connect4Game, userID string) int {
	switch userID {
	case game.Player1:
		return c4Player1
	case game.Player2:
		return c4Player2
	default:
		return c4Empty
	}
}

// c4Other returns the opposing player number.
func c4Other(player int) int {
	if player == c4Player1 {
		return c4Player2
	}
	return c4Player1
}

// c4NormalizeEmoji strips Unicode variation selectors (U+FE0F) so reaction
// matching works whether or not Discord includes them in the gateway payload.
func c4NormalizeEmoji(emoji string) string {
	return strings.ReplaceAll(emoji, "\uFE0F", "")
}

// c4ClearReactions best-effort removes every reaction from a finished game's
// message so players can't keep clicking dead buttons. Requires Manage Messages.
func c4ClearReactions(s *discordgo.Session, game *Connect4Game) {
	if s == nil || game.MessageID == "" {
		return
	}
	_ = s.MessageReactionsRemoveAll(game.ChannelID, game.MessageID)
}

func generateGameID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func c4GetUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func c4RespondEphemeral(s *discordgo.Session, i *discordgo.InteractionCreate, content string) {
	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
}
