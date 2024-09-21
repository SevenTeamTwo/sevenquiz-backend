package quiz

import (
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"sevenquiz-api/internal/config"
	"sevenquiz-api/internal/websocket"

	"github.com/golang-jwt/jwt"
	"github.com/lithammer/shortuuid/v3"
)

type Client struct {
	// Username is unique and used to define the lobby owner.
	Username string `json:"username"`

	// Score represents the user's quiz score
	Score float64 `json:"score"`

	alive bool
	mu    sync.Mutex
}

func (c *Client) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alive = false
}

func (c *Client) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive
}

func (c *Client) Connect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alive = true
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
	ID         string
	Owner      string
	MaxPlayers int

	// tokenValidity invalidates an access token if the "tokenValidity" claim
	// doesn't match. Since lobby ids are short-sized, it prevents previous
	// lobby owner/players from accessing a newly created lobby with the old token.
	tokenValidity string

	// clients represents all the active websockets in a lobby.
	// A client != nil means a conn has registered.
	clients map[*websocket.Conn]*Client
	created time.Time
	mu      sync.Mutex
	state   lobbyState
}

func (l *Lobby) Init() {
	l.tokenValidity = shortuuid.New()
	l.created = time.Now()
	l.clients = map[*websocket.Conn]*Client{}
	l.state = LobbyStateCreated
}

func (l *Lobby) NumConns() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.numConns()
}

func (l *Lobby) numConns() int {
	if _, ok := l.clients[nil]; ok {
		return len(l.clients) - 1
	}
	return len(l.clients)
}

func (l *Lobby) CloseConns() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.closeConns()
}

func (l *Lobby) closeConns() {
	for c := range l.clients {
		if c != nil {
			c.Close()
		}
	}
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

	players := make([]string, 0, l.numConns())
	for _, client := range l.clients {
		if client == nil || !client.Alive() {
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

func (l *Lobby) SetTokenValidity(tv string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.tokenValidity = tv
}

func (l *Lobby) AssignConn(client *Client, conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
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
	l.clients[newConn] = client

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
		"tokenValidity": l.tokenValidity,
		"username":      username,
	})
	return token.SignedString(cfg.JWTSecret)
}

func GetStringClaim(claims jwt.MapClaims, claim string) (string, bool) {
	claimAny, ok := claims[claim]
	if !ok {
		return "", false
	}
	claimStr, ok := claimAny.(string)

	return claimStr, ok
}

func JWTKeyFunc(cfg config.Config) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return cfg.JWTSecret, nil
	}
}

func (l *Lobby) CheckToken(cfg config.Config, token string) (jwt.MapClaims, error) {
	jwtToken, err := jwt.Parse(token, JWTKeyFunc(cfg))
	if err != nil {
		return nil, err
	}

	claimsMap, ok := jwtToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid jwt claims")
	}

	lobbyID, ok := GetStringClaim(claimsMap, "lobbyId")
	if !ok {
		return nil, errors.New("token has no lobbyId claim")
	}
	if lobbyID != l.ID {
		return nil, errors.New("token does not match lobby id")
	}

	tokenValidity, ok := GetStringClaim(claimsMap, "tokenValidity")
	if !ok {
		return nil, errors.New("token has no tokenValidity claim")
	}
	if tokenValidity != l.tokenValidity {
		return nil, errors.New("token does not match token validity")
	}

	return claimsMap, nil
}
