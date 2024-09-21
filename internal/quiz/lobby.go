package quiz

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"sevenquiz-api/internal/config"
	"sevenquiz-api/internal/websocket"

	"github.com/golang-jwt/jwt"
)

type Client struct {
	// Username is unique and used to define the lobby owner.
	Username string `json:"username"`

	// Score represents the user's quiz score
	Score float64 `json:"score"`

	disconnected bool
	mu           sync.Mutex
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnected = true
}

func (c *Client) IsDisconnected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.disconnected
}

func (c *Client) Reconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.disconnected = false
}

type lobbyState int

const (
	LobbyStateCreated lobbyState = iota
	LobbyStateRegister
	LobbyStateQuiz
	LobbyStateResponses
	LobbyStateEnded
)

type Lobby struct {
	ID         string    `json:"id"`
	Created    time.Time `json:"created"`
	Owner      string    `json:"owner"`
	MaxPlayers int       `json:"maxPlayers"`
	PlayerList []string  `json:"playerList"`

	// TokenValidity invalidates an access token if the "tokenValidity" claim
	// doesn't match. Since lobby ids are short-sized, it prevents previous
	// lobby owner/players from accessing a newly created lobby with the old token.
	TokenValidity string `json:"-"`

	// clients represents all the active websockets in a lobby.
	// A client != nil means a conn has registered.
	clients map[*websocket.Conn]*Client
	mu      sync.Mutex
	state   lobbyState
}

func (l *Lobby) NumConns() int {
	if _, ok := l.clients[nil]; ok {
		return len(l.clients) - 1
	}
	return len(l.clients)
}

func (l *Lobby) CloseConns() {
	for c := range l.clients {
		if c != nil {
			c.Close()
		}
	}
}

type jsonLobby Lobby

func (l *Lobby) MarshalJSON() ([]byte, error) {
	lobby := jsonLobby{
		ID:         l.ID,
		Created:    l.Created,
		Owner:      l.Owner,
		MaxPlayers: l.MaxPlayers,
		PlayerList: l.GetPlayerList(),
	}
	return json.Marshal(&lobby)
}

func (l *Lobby) GetClient(username string) (*websocket.Conn, *Client, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.getClient(username)
}

func (l *Lobby) getClient(username string) (*websocket.Conn, *Client, bool) {
	for conn, client := range l.clients {
		if client == nil {
			continue
		}
		if client.Username == username {
			return conn, client, true
		}
	}
	return nil, nil, false
}

func (l *Lobby) GetPlayerList() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	players := make([]string, 0, l.NumConns())
	for _, client := range l.clients {
		if client == nil || client.IsDisconnected() {
			continue
		}
		players = append(players, client.Username)
	}

	sort.Strings(players)

	return players
}

func (l *Lobby) GetClientFromConn(conn *websocket.Conn) *Client {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.clients[conn]
}

func (l *Lobby) SetState(state lobbyState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

func (l *Lobby) AssignConn(client *Client, conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.assignConn(client, conn)
}

func (l *Lobby) assignConn(client *Client, conn *websocket.Conn) {
	if l.clients == nil {
		l.clients = make(map[*websocket.Conn]*Client)
	}

	l.clients[conn] = client
}

// ReplaceConn replaces a conn for the specified client and
// returns the oldConn with a bool describing if a replace happened.
func (l *Lobby) ReplaceConn(client *Client, newConn *websocket.Conn) (oldConn *websocket.Conn, replaced bool) {
	if client == nil {
		return nil, false
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	oldConn, _, replaced = l.getClient(client.Username)
	if !replaced {
		return nil, replaced
	}
	if oldConn != nil {
		oldConn.Close()
	}

	l.deleteConn(oldConn)
	l.assignConn(client, newConn)

	return oldConn, replaced
}

func (l *Lobby) DeleteConn(conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.deleteConn(conn)
}

func (l *Lobby) deleteConn(conn *websocket.Conn) {
	if conn != nil {
		conn.Close()
	}
	delete(l.clients, conn)
}

func (l *Lobby) NewToken(cfg config.Config, username string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"lobbyId":       l.ID,
		"tokenValidity": l.TokenValidity,
		"username":      username,
	})
	return token.SignedString(cfg.JWTSecret)
}

func (l *Lobby) CheckToken(cfg config.Config, token string) (claims jwt.MapClaims, err error) {
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return cfg.JWTSecret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := jwtToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("could not assert jwt claims")
	}
	lobbyID, ok := claims["lobbyId"].(string)
	if !ok {
		return nil, errors.New("token has no lobbyId claim")
	}
	if lobbyID != l.ID {
		return nil, errors.New("token does not match lobby id")
	}
	tokenValidity, ok := claims["tokenValidity"].(string)
	if !ok {
		return nil, errors.New("token has no tokenValidity claim")
	}
	if tokenValidity != l.TokenValidity {
		return nil, errors.New("token does not match token validity")
	}

	return claims, nil
}
