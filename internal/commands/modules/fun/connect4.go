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

	c4ColorRed    = 0xE74C3C
	c4ColorYellow = 0xF1C40F
	c4ColorGreen  = 0x2ECC71
	c4ColorGray   = 0x95A5A6
	c4ColorBlue   = 0x3498DB
)

// Column reaction emoji — users react with these to pick a column.
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
	Turn         int    // c4Player1 or c4Player2
	ChannelID    string
	MessageID    string // the game board message
	Status       c4Status
	Winner       int // 0 = draw, c4Player1 or c4Player2
	WinReason    string
	LastActivity time.Time
}

// --- Manager ---

type connect4Manager struct {
	games     map[string]*Connect4Game
	msgToGame map[string]string // messageID → gameID for fast reaction lookup
	mu        sync.Mutex
	session   *discordgo.Session
	cfg       *config.Config
	once      sync.Once
}

var c4mgr = &connect4Manager{
	games:     make(map[string]*Connect4Game),
	msgToGame: make(map[string]string),
}

func (mgr *connect4Manager) init(s *discordgo.Session, cfg *config.Config) {
	mgr.once.Do(func() {
		mgr.session = s
		mgr.cfg = cfg
		go mgr.cleanupLoop()
	})
}

func (mgr *connect4Manager) createChallenge(player1, player2, channelID string) *Connect4Game {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	game := &Connect4Game{
		ID:           generateGameID(),
		Player1:      player1,
		Player2:      player2,
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
		expired := mgr.collectExpired()
		for _, game := range expired {
			mgr.updateGameMessage(game)
		}
		mgr.removeStale()
	}
}

func (mgr *connect4Manager) collectExpired() []*Connect4Game {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	var expired []*Connect4Game
	now := time.Now()

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
				game.WinReason = fmt.Sprintf("⏰ <@%s> took too long — %s <@%s> wins!", game.Player1, c4EmojiPlayer2, game.Player2)
			} else {
				game.Winner = c4Player1
				game.WinReason = fmt.Sprintf("⏰ <@%s> took too long — %s <@%s> wins!", game.Player2, c4EmojiPlayer1, game.Player1)
			}
		}
		expired = append(expired, game)
	}
	return expired
}

func (mgr *connect4Manager) removeStale() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	now := time.Now()
	for id, game := range mgr.games {
		if game.Status == c4Finished && now.Sub(game.LastActivity) > 5*time.Minute {
			delete(mgr.msgToGame, game.MessageID)
			delete(mgr.games, id)
		}
	}
}

// updateGameMessage edits the game's Discord message with the current state.
func (mgr *connect4Manager) updateGameMessage(game *Connect4Game) {
	if mgr.session == nil || game.MessageID == "" {
		return
	}
	embed := c4BuildGameEmbed(game)
	components := c4BuildComponents(game)
	_, _ = mgr.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		Channel:    game.ChannelID,
		ID:         game.MessageID,
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
			switch game.Board[row][col] {
			case c4Player1:
				sb.WriteString(c4EmojiPlayer1)
			case c4Player2:
				sb.WriteString(c4EmojiPlayer2)
			default:
				sb.WriteString(c4EmojiEmpty)
			}
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

	switch game.Status {
	case c4Pending:
		embed.Title = "⚔️ Connect 4 Challenge"
		embed.Footer.Text = fmt.Sprintf("Game %s", game.ID)
		desc.WriteString(fmt.Sprintf("%s <@%s> has challenged %s <@%s> to a game of Connect 4!\n\n", c4EmojiPlayer1, game.Player1, c4EmojiPlayer2, game.Player2))
		desc.WriteString(fmt.Sprintf("Waiting for <@%s> to respond...", game.Player2))
		embed.Color = c4ColorBlue

	case c4Active:
		embed.Title = "🎮 Connect 4"
		desc.WriteString(fmt.Sprintf("%s <@%s>  vs  %s <@%s>\n\n", c4EmojiPlayer1, game.Player1, c4EmojiPlayer2, game.Player2))
		desc.WriteString(c4RenderBoard(game))
		if game.Turn == c4Player1 {
			desc.WriteString(fmt.Sprintf("\n%s <@%s>'s turn", c4EmojiPlayer1, game.Player1))
			embed.Color = c4ColorRed
		} else {
			desc.WriteString(fmt.Sprintf("\n%s <@%s>'s turn", c4EmojiPlayer2, game.Player2))
			embed.Color = c4ColorYellow
		}

	case c4Finished:
		embed.Title = "🎮 Connect 4"
		embed.Footer.Text = fmt.Sprintf("Game %s • Game over", game.ID)
		desc.WriteString(fmt.Sprintf("%s <@%s>  vs  %s <@%s>\n\n", c4EmojiPlayer1, game.Player1, c4EmojiPlayer2, game.Player2))
		desc.WriteString(c4RenderBoard(game))
		desc.WriteString("\n")
		if game.WinReason != "" {
			desc.WriteString(game.WinReason)
		} else if game.Winner == c4Player1 {
			desc.WriteString(fmt.Sprintf("🎉 %s <@%s> wins!", c4EmojiPlayer1, game.Player1))
		} else if game.Winner == c4Player2 {
			desc.WriteString(fmt.Sprintf("🎉 %s <@%s> wins!", c4EmojiPlayer2, game.Player2))
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
	_ = s.MessageReactionAdd(channelID, messageID, "🏳️")
}

// --- Slash Command Handler ---

func (m *Module) handleConnect4Challenge(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c4mgr.init(s, m.config)

	var opponent *discordgo.User
	for _, opt := range i.ApplicationCommandData().Options {
		if opt.Name == "opponent" {
			opponent = opt.UserValue(s)
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

	game := c4mgr.createChallenge(challenger.ID, opponent.ID, i.ChannelID)
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
	game.LastActivity = time.Now()
	c4mgr.mu.Unlock()

	embed := c4BuildGameEmbed(game)

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
	c4mgr.mu.Unlock()

	embed := c4BuildGameEmbed(game)

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

// HandleReactionAdd processes a reaction on a Connect 4 game message.
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

	// Always remove the user's reaction to keep the board clean
	defer func() {
		_ = s.MessageReactionRemove(r.ChannelID, r.MessageID, r.Emoji.APIName(), r.UserID)
	}()

	if game.Status != c4Active {
		return
	}

	// Handle forfeit
	if r.Emoji.Name == "🏳️" {
		c4HandleReactionForfeit(s, game, r.UserID)
		return
	}

	// Determine column from emoji
	col := -1
	for i, emoji := range c4ColumnEmoji {
		if r.Emoji.Name == emoji {
			col = i
			break
		}
	}
	if col == -1 {
		return
	}

	c4HandleReactionMove(s, game, r.UserID, col)
}

func c4HandleReactionMove(s *discordgo.Session, game *Connect4Game, userID string, col int) {
	var player int
	if userID == game.Player1 {
		player = c4Player1
	} else if userID == game.Player2 {
		player = c4Player2
	} else {
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

	game.LastActivity = time.Now()

	if c4CheckWin(game, row, col, player) {
		game.Status = c4Finished
		game.Winner = player
	} else if c4CheckDraw(game) {
		game.Status = c4Finished
		game.Winner = 0
	} else {
		if game.Turn == c4Player1 {
			game.Turn = c4Player2
		} else {
			game.Turn = c4Player1
		}
	}

	c4mgr.mu.Unlock()

	c4mgr.updateGameMessage(game)
}

func c4HandleReactionForfeit(s *discordgo.Session, game *Connect4Game, userID string) {
	c4mgr.mu.Lock()

	if game.Status != c4Active {
		c4mgr.mu.Unlock()
		return
	}

	if userID == game.Player1 {
		game.Winner = c4Player2
		game.WinReason = fmt.Sprintf("🏳️ <@%s> forfeited — %s <@%s> wins!", game.Player1, c4EmojiPlayer2, game.Player2)
	} else if userID == game.Player2 {
		game.Winner = c4Player1
		game.WinReason = fmt.Sprintf("🏳️ <@%s> forfeited — %s <@%s> wins!", game.Player2, c4EmojiPlayer1, game.Player1)
	} else {
		c4mgr.mu.Unlock()
		return
	}

	game.Status = c4Finished
	game.LastActivity = time.Now()
	c4mgr.mu.Unlock()

	c4mgr.updateGameMessage(game)
}

// --- Helpers ---

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
