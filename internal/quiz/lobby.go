package quiz

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"sort"
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
	id         string
	owner      string
	maxPlayers int
	quizzes    map[string]api.Quiz
	quiz       api.Quiz
	question   *api.Question
	password   string

	// players represents all the active players in a lobby.
	// A LobbyPlayer != nil means a websocket has issued the register cmd.
	players map[*websocket.Conn]*Player

	jwtKey  []byte
	created time.Time
	mu      sync.RWMutex
	state   LobbyState
	doneCh  chan struct{}
}

// Close shutdowns a lobby and closes all registered websockets.
func (l *Lobby) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var err error
	for c := range l.players {
		if c != nil {
			err2 := c.Close(websocket.StatusNormalClosure, "lobby closes")
			if err == nil && err2 != nil {
				err = err2
			}
		}
	}

	close(l.doneCh)

	return err
}

// CloseUnregisteredConns shutdowns all websockets that did not register as a player.
func (l *Lobby) CloseUnregisteredConns() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	var err error
	for c, player := range l.allPlayers(false) {
		if player == nil {
			err2 := c.Close(websocket.StatusNormalClosure, "closing of unregistered conns")
			if err == nil && err2 != nil {
				err = err2
			}
		}
	}

	return err
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
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.owner
}

// SetOwner update the current lobby owner.
func (l *Lobby) SetOwner(username string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.owner = username
}

// CheckPassword checks if the input password is valid.
func (l *Lobby) CheckPassword(password string) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if l.password == "" {
		return true
	}
	return password == l.password
}

// SetPassword sets a lobby password.
func (l *Lobby) SetPassword(password string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.password = password
}

// State returns the current lobby state.
func (l *Lobby) State() LobbyState {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.state
}

// SetState updates a lobby state.
func (l *Lobby) SetState(state LobbyState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

// SetCurrentQuestion updates a lobby question.
func (l *Lobby) SetCurrentQuestion(question *api.Question) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.question = question
}

func (l *Lobby) CurrentQuestion() *api.Question {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.question
}

// CreationDate returns when a lobby was originally created.
func (l *Lobby) CreationDate() time.Time {
	return l.created
}

// MaxPlayers returns the maximum allowed players in a lobby.
func (l *Lobby) MaxPlayers() int {
	return l.maxPlayers
}

func (l *Lobby) Quiz() api.Quiz {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.quiz
}

func (l *Lobby) SetQuiz(quiz api.Quiz) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.quiz = quiz
}

func (l *Lobby) LoadQuiz(quiz string) (api.Quiz, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	q, ok := l.quizzes[quiz]
	return q, ok
}

func (l *Lobby) ListQuizzes() []string {
	return l.listQuizzes()
}

func (l *Lobby) listQuizzes() []string {
	quizzes := make([]string, 0, len(l.quizzes))

	for name := range l.quizzes {
		quizzes = append(quizzes, name)
	}

	sort.Strings(quizzes)

	return quizzes
}

// IsFull checks the total number of registered websockets in a
// lobby and returns true if it exceeds the lobby's max players.
func (l *Lobby) IsFull() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.maxPlayers >= 0 && l.numConns() >= l.maxPlayers
}

// NumConns returns the number of websockets registered in a lobby.
func (l *Lobby) NumConns() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.numConns()
}

func (l *Lobby) numConns() int {
	return len(l.players)
}

// GetPlayer finds a user by username and returns his associated websocket.
// A third return value specifies if a player was found.
func (l *Lobby) GetPlayer(username string) (*websocket.Conn, *Player, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.getPlayer(username)
}

func (l *Lobby) getPlayer(username string) (*websocket.Conn, *Player, bool) {
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
	l.mu.RLock()
	defer l.mu.RUnlock()

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
func (l *Lobby) GetPlayerByConn(conn *websocket.Conn) (*Player, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	c, ok := l.players[conn]
	return c, ok
}

// AddPlayerWithConn registers a conn to a lobby player.
func (l *Lobby) AddPlayerWithConn(conn *websocket.Conn, username string) *Player {
	l.mu.Lock()
	defer l.mu.Unlock()

	cli := &Player{username: username, alive: true}
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

func (l *Lobby) allPlayers(registeredOnly bool) iter.Seq2[*websocket.Conn, *Player] {
	return func(yield func(*websocket.Conn, *Player) bool) {
		for i, v := range l.players {
			if i == nil {
				continue
			}
			if registeredOnly && v == nil {
				continue
			}
			if !yield(i, v) {
				return
			}
		}
	}
}

// BroadcastPlayerUpdate broadcast a player event to all players
// and websockets active in the lobby.
func (l *Lobby) BroadcastPlayerUpdate(ctx context.Context, username, action string) error {
	return l.Broadcast(ctx, func(_ *Player) any {
		return api.Response[api.PlayerUpdateResponseData]{
			Type: api.ResponseTypePlayerUpdate,
			Data: api.PlayerUpdateResponseData{
				Username: username,
				Action:   action,
			},
		}
	})
}

func (l *Lobby) BroadcastConfigure(ctx context.Context, quiz string) error {
	return l.Broadcast(ctx, func(_ *Player) any {
		return api.Response[api.LobbyUpdateResponseData]{
			Type: api.ResponseTypeConfigure,
			Data: api.LobbyUpdateResponseData{
				Quiz: quiz,
			},
		}
	})
}

func (l *Lobby) BroadcastQuestion(ctx context.Context, question api.Question) error {
	return l.Broadcast(ctx, func(_ *Player) any {
		return api.Response[api.QuestionResponseData]{
			Type: api.ResponseTypeQuestion,
			Data: api.QuestionResponseData{
				Question: question,
			},
		}
	})
}

func (l *Lobby) Broadcast(ctx context.Context, fn func(player *Player) any) error {
	l.mu.RLock()
	defer l.mu.RUnlock()

	errs := errgroup.Group{}
	for conn, player := range l.allPlayers(true) {
		errs.Go(func() error {
			res := fn(player)
			err := wsjson.Write(ctx, conn, res)
			if err != nil && player != nil {
				err = fmt.Errorf("%s: %w", player.username, err)
			}
			return err
		})
	}

	return errs.Wait()
}

func (l *Lobby) BroadcastStart(ctx context.Context) error {
	return l.Broadcast(ctx, func(player *Player) any {
		token, err := l.NewToken(player.Username())
		if err != nil {
			return err
		}
		return api.Response[api.StartResponseData]{
			Type: api.ResponseTypeStart,
			Data: api.StartResponseData{
				Token: token,
			},
		}
	})
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
