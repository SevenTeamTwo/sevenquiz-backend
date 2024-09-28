package quiz

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"sevenquiz-backend/api"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/golang-jwt/jwt"
	"golang.org/x/sync/errgroup"
)

type LobbyState int

const (
	LobbyStateCreated LobbyState = iota
	LobbyStateRegister
	LobbyStateQuiz
	LobbyStateAnswers
	LobbyStateEnded
)

var lobbyStateToString = map[LobbyState]string{
	LobbyStateCreated:  "created",
	LobbyStateRegister: "register",
	LobbyStateQuiz:     "quiz",
	LobbyStateAnswers:  "answers",
	LobbyStateEnded:    "ended",
}

func (ls LobbyState) String() string {
	if s, ok := lobbyStateToString[ls]; ok {
		return s
	}
	return "unknown"
}

// Lobby represents a player lobby identified by their associated websocket.
//
// Multiple goroutines may invoke methods on a Lobby simultaneously.
type Lobby struct {
	id           string
	owner        string
	maxPlayers   int
	quizzes      fs.FS
	selectedQuiz string

	// players represents all the active players in a lobby.
	// A LobbyPlayer != nil means a websocket has issued the register cmd.
	players map[*websocket.Conn]*LobbyPlayer

	jwtKey  []byte
	created time.Time
	mu      sync.Mutex
	state   LobbyState
	doneCh  chan struct{}
}

// Close shutdowns a lobby and closes all registered websockets.
func (l *Lobby) Close() {
	l.mu.Lock()
	defer l.mu.Unlock()

	for c := range l.players {
		if c != nil {
			c.Close(websocket.StatusNormalClosure, "lobby closes")
		}
	}

	close(l.doneCh)
}

// Done returns if a lobby has been closed.
func (l *Lobby) Done() <-chan struct{} {
	return l.doneCh
}

// ID returns the lobby unique id.
func (l *Lobby) ID() string {
	return l.id
}

// Owner returns the current lobby owner.
func (l *Lobby) Owner() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.owner
}

// SetOwner update the current lobby owner.
func (l *Lobby) SetOwner(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owner = username
}

// State returns the current lobby state.
func (l *Lobby) State() LobbyState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}

// SetState updates a lobby state.
func (l *Lobby) SetState(state LobbyState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

// CreationDate returns when a lobby was originally created.
func (l *Lobby) CreationDate() time.Time {
	return l.created
}

// MaxPlayers returns the maximum allowed players in a lobby.
func (l *Lobby) MaxPlayers() int {
	return l.maxPlayers
}

func (l *Lobby) Quiz() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.selectedQuiz
}

func (l *Lobby) SetQuiz(quiz string) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	quizzes, err := l.listQuizzes()
	if err != nil {
		return err
	}
	if !slices.Contains(quizzes, quiz) {
		return errors.New("quiz does not exists")
	}

	l.selectedQuiz = quiz

	return nil
}

func (l *Lobby) ListQuizzes() ([]string, error) {
	return l.listQuizzes()
}

func (l *Lobby) listQuizzes() ([]string, error) {
	var quizzes []string

	root := "."
	depth := 0

	err := fs.WalkDir(l.quizzes, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() && strings.Count(path, "/") <= depth {
			quizzes = append(quizzes, d.Name())
		}
		return nil
	})

	return quizzes, err
}

// IsFull checks the total number of registered websockets in a
// lobby and returns true if it exceeds the lobby's max players.
func (l *Lobby) IsFull() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.maxPlayers >= 0 && l.numConns() >= l.maxPlayers
}

// NumConns returns the number of websockets registered in a lobby.
func (l *Lobby) NumConns() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.numConns()
}

func (l *Lobby) numConns() int {
	if _, ok := l.players[nil]; ok {
		return len(l.players) - 1
	}
	return len(l.players)
}

// GetPlayer finds a user by username and returns his associated websocket.
// A third return value specifies if a player was found.
func (l *Lobby) GetPlayer(username string) (*websocket.Conn, *LobbyPlayer, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.getPlayer(username)
}

func (l *Lobby) getPlayer(username string) (*websocket.Conn, *LobbyPlayer, bool) {
	for conn, client := range l.players {
		if client == nil {
			continue
		}
		if client.username == username {
			return conn, client, true
		}
	}
	return nil, nil, false
}

// GetPlayerList returns the current lobby player list.
func (l *Lobby) GetPlayerList() []string {
	l.mu.Lock()
	defer l.mu.Unlock()

	players := make([]string, 0, l.numConns())
	for _, client := range l.players {
		if client == nil || !client.Alive() {
			continue
		}
		players = append(players, client.username)
	}

	sort.Strings(players)

	return players
}

// GetPlayerByConn finds a player by his associated websocket.
// A second return value specifies if the conn was associated to a lobby player.
func (l *Lobby) GetPlayerByConn(conn *websocket.Conn) (*LobbyPlayer, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	c, ok := l.players[conn]
	return c, ok
}

// AddPlayerWithConn registers a conn to a lobby player.
func (l *Lobby) AddPlayerWithConn(conn *websocket.Conn, username string) *LobbyPlayer {
	l.mu.Lock()
	defer l.mu.Unlock()

	cli := &LobbyPlayer{username: username, alive: true}
	l.players[conn] = cli

	return cli
}

// AddConn registers a new websocket in the lobby that is not associated
// to a lobby player yet.
func (l *Lobby) AddConn(conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.players[conn] = nil
}

// Broadcast sends a JSON message to all players and websockets
// active in the lobby.
func (l *Lobby) Broadcast(ctx context.Context, v any) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	errs := errgroup.Group{}
	for conn := range l.players {
		errs.Go(func() error {
			if conn == nil {
				return nil
			}
			return wsjson.Write(ctx, conn, v)
		})
	}
	return errs.Wait()
}

// BroadcastPlayerUpdate broadcast a player event to all players
// and websockets active in the lobby.
func (l *Lobby) BroadcastPlayerUpdate(ctx context.Context, username, action string) error {
	res := api.Response[api.PlayerUpdateResponseData]{
		Type: api.ResponseTypePlayerUpdate,
		Data: api.PlayerUpdateResponseData{
			Username: username,
			Action:   action,
		},
	}
	return l.Broadcast(ctx, res)
}

func (l *Lobby) BroadcastConfigure(ctx context.Context, quiz string) error {
	res := api.Response[api.LobbyConfigureResponseData]{
		Type: api.ResponseTypeConfigure,
		Data: api.LobbyConfigureResponseData{
			Quiz: quiz,
		},
	}
	return l.Broadcast(ctx, res)
}

// ReplacePlayerConn replaces a conn for the specified player and
// returns the oldConn with a bool describing if a replace happened.
func (l *Lobby) ReplacePlayerConn(username string, newConn *websocket.Conn) (oldConn *websocket.Conn, replaced bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	oldConn, client, replaced := l.getPlayer(username)
	if !replaced {
		return nil, replaced
	}
	if oldConn != nil {
		oldConn.CloseNow()
	}

	l.deleteConn(oldConn)
	l.players[newConn] = client

	client.Connect()

	return oldConn, replaced
}

// DeletePlayer finds a player by username, closes his websocket and
// removes the player from the lobby.
// It returns false if the player does not exists.
func (l *Lobby) DeletePlayer(username string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.deletePlayer(username)
}

func (l *Lobby) deletePlayer(username string) bool {
	conn, _, ok := l.getPlayer(username)
	if !ok {
		return false
	}
	if conn != nil {
		conn.CloseNow()
	}
	delete(l.players, conn)
	return true
}

// DeletePlayerByConn removes a player in the lobby by finding
// the associated websocket.
func (l *Lobby) DeletePlayerByConn(conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.deleteConn(conn)
}

func (l *Lobby) deleteConn(conn *websocket.Conn) {
	if conn != nil {
		conn.CloseNow()
	}
	delete(l.players, conn)
}

// NewToken generates a new jwt token associated to a username.
func (l *Lobby) NewToken(username string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"lobbyId":  l.id,
		"username": username,
	})
	return token.SignedString(l.jwtKey)
}

// CheckToken validates a token against the configured jwt secret.
//
// A check fails if the lobbyId doesn't match the associated lobby.
func (l *Lobby) CheckToken(token string) (jwt.MapClaims, error) {
	jwtToken, err := jwt.Parse(token, jwtKeyFunc(l.jwtKey))
	if err != nil {
		return nil, err
	}
	claimsMap, ok := jwtToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid jwt claims")
	}
	lobbyID, ok := getStringClaim(claimsMap, "lobbyId")
	if !ok {
		return nil, errors.New("token has no lobbyId claim")
	}
	if lobbyID != l.id {
		return nil, errors.New("token does not match lobby id")
	}
	return claimsMap, nil
}

func getStringClaim(claims jwt.MapClaims, claim string) (string, bool) {
	claimAny, ok := claims[claim]
	if !ok {
		return "", false
	}
	claimStr, ok := claimAny.(string)
	return claimStr, ok
}

func jwtKeyFunc(key []byte) jwt.Keyfunc {
	return func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return key, nil
	}
}

// LobbyPlayer represents a registered used in a lobby
//
// Multiple goroutines may invoke methods on a LobbyPlayer simultaneously.
type LobbyPlayer struct {
	username string
	score    float64
	alive    bool
	mu       sync.Mutex
}

func (c *LobbyPlayer) Username() string {
	return c.username
}

func (c *LobbyPlayer) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alive = false
}

func (c *LobbyPlayer) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive
}

func (c *LobbyPlayer) Connect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alive = true
}
