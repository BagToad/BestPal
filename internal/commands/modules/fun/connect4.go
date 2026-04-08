package fun

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
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
	MessageID    string
	Status       c4Status
	Winner       int // 0 = draw, c4Player1 or c4Player2
	WinReason    string
	LastActivity time.Time
}

// --- Manager ---

type connect4Manager struct {
	games   map[string]*Connect4Game
	mu      sync.Mutex
	session *discordgo.Session
	cfg     *config.Config
	once    sync.Once
}

var c4mgr = &connect4Manager{
	games: make(map[string]*Connect4Game),
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

func (mgr *connect4Manager) getGame(id string) *Connect4Game {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.games[id]
}

// cleanupLoop periodically checks for timed-out and stale games.
func (mgr *connect4Manager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		expired := mgr.collectExpired()
		for _, game := range expired {
			embed := c4BuildGameEmbed(game)
			_, _ = mgr.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
				Channel:    game.ChannelID,
				ID:         game.MessageID,
				Embeds:     &[]*discordgo.MessageEmbed{embed},
				Components: &[]discordgo.MessageComponent{},
			})
		}
		mgr.removeStale()
	}
}

// collectExpired marks timed-out games as finished and returns them for message updates.
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

// removeStale deletes finished games that have been idle for 5+ minutes.
func (mgr *connect4Manager) removeStale() {
	mgr.mu.Lock()
	defer mgr.mu.Unlock()

	now := time.Now()
	for id, game := range mgr.games {
		if game.Status == c4Finished && now.Sub(game.LastActivity) > 5*time.Minute {
			delete(mgr.games, id)
		}
	}
}

// --- Game Logic ---

// dropPiece drops a piece in the given column. Returns the row it landed on, or -1 if the column is full.
func c4DropPiece(game *Connect4Game, col, player int) int {
	for row := c4Rows - 1; row >= 0; row-- {
		if game.Board[row][col] == c4Empty {
			game.Board[row][col] = player
			return row
		}
	}
	return -1
}

// checkWin checks if the last move at (row, col) created a four-in-a-row.
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

// checkDraw returns true if the board is completely full.
func c4CheckDraw(game *Connect4Game) bool {
	for col := 0; col < c4Cols; col++ {
		if game.Board[0][col] == c4Empty {
			return false
		}
	}
	return true
}

// --- Rendering ---

func c4RenderBoard(game *Connect4Game) string {
	var sb strings.Builder
	for row := 0; row < c4Rows; row++ {
		for col := 0; col < c4Cols; col++ {
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
	return sb.String()
}

func c4BuildGameEmbed(game *Connect4Game) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Footer: &discordgo.MessageEmbedFooter{Text: fmt.Sprintf("Game %s", game.ID)},
	}

	var desc strings.Builder

	switch game.Status {
	case c4Pending:
		embed.Title = "⚔️ Connect 4 Challenge"
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

func c4BuildChallengeComponents(gameID string) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Accept",
					Style:    discordgo.SuccessButton,
					CustomID: fmt.Sprintf("c4:a:%s", gameID),
					Emoji:    &discordgo.ComponentEmoji{Name: "✅"},
				},
				discordgo.Button{
					Label:    "Decline",
					Style:    discordgo.DangerButton,
					CustomID: fmt.Sprintf("c4:d:%s", gameID),
					Emoji:    &discordgo.ComponentEmoji{Name: "❌"},
				},
			},
		},
	}
}

func c4BuildGameComponents(game *Connect4Game) []discordgo.MessageComponent {
	if game.Status == c4Finished {
		return []discordgo.MessageComponent{}
	}

	// Row 1: columns 1-5
	row1 := discordgo.ActionsRow{Components: make([]discordgo.MessageComponent, 5)}
	for i := 0; i < 5; i++ {
		row1.Components[i] = discordgo.Button{
			Label:    fmt.Sprintf("%d", i+1),
			Style:    discordgo.SecondaryButton,
			CustomID: fmt.Sprintf("c4:m:%s:%d", game.ID, i),
			Disabled: game.Board[0][i] != c4Empty,
		}
	}

	// Row 2: columns 6-7
	row2 := discordgo.ActionsRow{Components: make([]discordgo.MessageComponent, 2)}
	for i := 5; i < 7; i++ {
		row2.Components[i-5] = discordgo.Button{
			Label:    fmt.Sprintf("%d", i+1),
			Style:    discordgo.SecondaryButton,
			CustomID: fmt.Sprintf("c4:m:%s:%d", game.ID, i),
			Disabled: game.Board[0][i] != c4Empty,
		}
	}

	// Row 3: forfeit
	row3 := discordgo.ActionsRow{
		Components: []discordgo.MessageComponent{
			discordgo.Button{
				Label:    "Forfeit",
				Style:    discordgo.DangerButton,
				CustomID: fmt.Sprintf("c4:f:%s", game.ID),
				Emoji:    &discordgo.ComponentEmoji{Name: "🏳️"},
			},
		},
	}

	return []discordgo.MessageComponent{row1, row2, row3}
}

// --- Interaction Handlers ---

func (m *FunModule) handleConnect4Challenge(s *discordgo.Session, i *discordgo.InteractionCreate) {
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
	components := c4BuildChallengeComponents(game.ID)

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

	// Store message ID so we can edit it later (timeout cleanup)
	msg, err := s.InteractionResponse(i.Interaction)
	if err == nil {
		c4mgr.mu.Lock()
		game.MessageID = msg.ID
		c4mgr.mu.Unlock()
	}
}

// HandleComponent routes Connect 4 component interactions.
func (m *FunModule) HandleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	c4mgr.init(s, m.config)

	cid := i.MessageComponentData().CustomID
	parts := strings.SplitN(cid, ":", 4) // c4:action:gameID[:data]
	if len(parts) < 3 {
		return
	}

	action := parts[1]
	gameID := parts[2]

	switch action {
	case "a":
		m.handleC4Accept(s, i, gameID)
	case "d":
		m.handleC4Decline(s, i, gameID)
	case "m":
		if len(parts) < 4 {
			return
		}
		col, err := strconv.Atoi(parts[3])
		if err != nil || col < 0 || col >= c4Cols {
			return
		}
		m.handleC4Move(s, i, gameID, col)
	case "f":
		m.handleC4Forfeit(s, i, gameID)
	}
}

func (m *FunModule) handleC4Accept(s *discordgo.Session, i *discordgo.InteractionCreate, gameID string) {
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
	components := c4BuildGameComponents(game)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Content:    "",
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func (m *FunModule) handleC4Decline(s *discordgo.Session, i *discordgo.InteractionCreate, gameID string) {
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

func (m *FunModule) handleC4Move(s *discordgo.Session, i *discordgo.InteractionCreate, gameID string, col int) {
	game := c4mgr.getGame(gameID)
	if game == nil {
		c4RespondEphemeral(s, i, "❌ This game no longer exists.")
		return
	}

	userID := c4GetUserID(i)

	var player int
	if userID == game.Player1 {
		player = c4Player1
	} else if userID == game.Player2 {
		player = c4Player2
	} else {
		c4RespondEphemeral(s, i, "❌ You're not a player in this game.")
		return
	}

	c4mgr.mu.Lock()

	if game.Status != c4Active {
		c4mgr.mu.Unlock()
		c4RespondEphemeral(s, i, "❌ This game is not active.")
		return
	}
	if game.Turn != player {
		c4mgr.mu.Unlock()
		c4RespondEphemeral(s, i, "❌ It's not your turn!")
		return
	}

	row := c4DropPiece(game, col, player)
	if row == -1 {
		c4mgr.mu.Unlock()
		c4RespondEphemeral(s, i, "❌ That column is full!")
		return
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

	embed := c4BuildGameEmbed(game)
	components := c4BuildGameComponents(game)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: components,
		},
	})
}

func (m *FunModule) handleC4Forfeit(s *discordgo.Session, i *discordgo.InteractionCreate, gameID string) {
	game := c4mgr.getGame(gameID)
	if game == nil {
		c4RespondEphemeral(s, i, "❌ This game no longer exists.")
		return
	}

	userID := c4GetUserID(i)

	c4mgr.mu.Lock()

	if game.Status != c4Active {
		c4mgr.mu.Unlock()
		c4RespondEphemeral(s, i, "❌ This game is not active.")
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
		c4RespondEphemeral(s, i, "❌ You're not a player in this game.")
		return
	}

	game.Status = c4Finished
	game.LastActivity = time.Now()

	c4mgr.mu.Unlock()

	embed := c4BuildGameEmbed(game)

	_ = s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseUpdateMessage,
		Data: &discordgo.InteractionResponseData{
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: []discordgo.MessageComponent{},
		},
	})
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
