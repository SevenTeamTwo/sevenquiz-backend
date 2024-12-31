package quiz

import (
	"errors"
	"fmt"
	"io/fs"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/lithammer/shortuuid/v3"
)

type lobbies struct {
	lobbies map[string]*Lobby
	mu      sync.Mutex
}

// NewLobbiesCache returns an in-memory storage of quiz lobbies.
func NewLobbiesCache() LobbyRepository {
	return &lobbies{
		lobbies: map[string]*Lobby{},
	}
}

var errNoLobbySlotAvailable = errors.New("no lobby slot available")

type LobbyOptions struct {
	// Owner represents the lobby's owner.
	//
	// This priviledged user has the rights to start a lobby and credit
	// points at the end of the quiz.
	//
	// Empty value will grant the first user to register owner privileges.
	Owner string

	// MaxPlayers defines the maximum amount of players allowed to join a lobby.
	// This limit is reached even with a lobby filled with unregistered users.
	//
	// Default is set to 25. Negative value means no limit.
	MaxPlayers int

	// Quizzes registers an embed filesystem holding all quizzes questions and assets.
	// All quizzes folders must be at filesystem's root directory.
	Quizzes fs.FS

	// JWTSalt is an optional salt to be used while generating the lobby's jwt key.
	//
	// It helps making the key more unique otherwise only a combination of
	// the ID and timestamp is used.
	JWTSalt []byte

	// Timeout sets a duration before a lobby expires.
	// A lobby expires if his state is still Created or Registered after timeout.
	//
	// Default is 15 minutes. Set a negative value to disable it.
	Timeout time.Duration

	// Password sets a lobby password to be check with lobby.CheckPassword().
	Password string
}

type LobbyRepository interface {
	Register(opts LobbyOptions) (*Lobby, error)
	Get(id string) (*Lobby, bool)
	Delete(id string)
}

// Register tries to register a new lobby and returns an error
// if no slots are available.
func (l *lobbies) Register(opts LobbyOptions) (*Lobby, error) {
	if opts.MaxPlayers == 0 {
		opts.MaxPlayers = 25
	}
	if opts.Timeout == 0 {
		opts.Timeout = 15 * time.Minute
	}

	id := newLobbyID()
	created := time.Now()

	lobby := &Lobby{
		id:         id,
		owner:      opts.Owner,
		maxPlayers: opts.MaxPlayers,
		quizzes:    opts.Quizzes,
		password:   opts.Password,
		jwtKey:     newLobbyTokenKey(opts.JWTSalt, id, created),
		players:    map[*websocket.Conn]*Player{},
		created:    created,
		state:      LobbyStateCreated,
		doneCh:     make(chan struct{}),
	}

	quizzes, err := lobby.listQuizzes()
	if err != nil {
		return nil, err
	}
	if len(quizzes) == 0 {
		return nil, errors.New("lobby has no quizzes")
	}

	lobby.selectedQuiz = quizzes[0]

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.lobbies == nil {
		l.lobbies = map[string]*Lobby{}
	}

	retries := 50
	for retries > 0 {
		if _, exist := l.lobbies[lobby.id]; !exist {
			break
		}
		lobby.id = newLobbyID()

		retries--
	}
	if retries <= 0 {
		return nil, errNoLobbySlotAvailable
	}

	l.lobbies[lobby.id] = lobby

	go l.lobbyTimeout(lobby, opts.Timeout)

	return lobby, nil
}

func (l *lobbies) lobbyTimeout(lobby *Lobby, timeout time.Duration) {
	select {
	case <-lobby.Done():
		return
	case <-time.After(timeout):
		switch lobby.State() {
		case LobbyStateCreated, LobbyStateRegister:
			// TODO: broadcast to conns before ?
			l.Delete(lobby.ID())
		}
	}
}

func newLobbyID() string {
	shortid := shortuuid.New()
	return shortid[:5]
}

// newLobbyTokenKey creates a dedicated jwt key associated to a lobby.
func newLobbyTokenKey(secret []byte, id string, created time.Time) []byte {
	key := fmt.Sprintf("%s%s%d", secret, id, created.Unix())
	hexkey := fmt.Sprintf("%x", key)
	return []byte(hexkey)
}

// Get retrieves a lobby by unique id.
func (l *lobbies) Get(id string) (*Lobby, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lobby, ok := l.lobbies[id]
	return lobby, ok
}

// Delete closes all lobby conns before deleting it.
func (l *lobbies) Delete(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lobby := l.lobbies[id]; lobby != nil {
		lobby.Close()
	}

	delete(l.lobbies, id)
}
