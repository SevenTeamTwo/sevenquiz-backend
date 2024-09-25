package quiz

import (
	"errors"
	"io/fs"
	"sevenquiz-backend/internal/websocket"
	"sync"
	"time"

	"github.com/lithammer/shortuuid/v3"
)

// Lobbies acts as an in-memory container for the quiz lobbies.
type Lobbies struct {
	lobbies map[string]*Lobby
	mu      sync.Mutex
}

var errNoLobbySlotAvailable = errors.New("no lobby slot available")

type LobbyOptions struct {
	// Owner represents the lobby's owner.
	// This priviledged user has the rights to start a lobby
	// and credit points at the end of the quiz.
	// Empty value will grant the first user to register owner privileges.
	Owner string

	// MaxPlayers defines the maximum amount of players
	// allowed to join a lobby. This limit is reached even
	// with a lobby filled with unregistered users.
	// Default is set to 25. Negative value means no limit.
	MaxPlayers int

	// Quizzes registers an embed filesystem holding all quizzes
	// questions and assets.
	Quizzes fs.FS
}

// Register tries to register a new lobby and returns an error
// if no slots are available.
func (l *Lobbies) Register(opts LobbyOptions) (*Lobby, error) {
	if opts.MaxPlayers == 0 {
		opts.MaxPlayers = 25
	}

	lobby := &Lobby{
		id:            newLobbyID(),
		owner:         opts.Owner,
		maxPlayers:    opts.MaxPlayers,
		quizzes:       opts.Quizzes,
		tokenValidity: shortuuid.New(),
		players:       map[*websocket.Conn]*LobbyPlayer{},
		created:       time.Now(),
		state:         LobbyStateCreated,
		doneCh:        make(chan struct{}),
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

	return lobby, nil
}

func newLobbyID() string {
	shortid := shortuuid.New()
	return shortid[:5]
}

// Get retrieves a lobby by unique id.
func (l *Lobbies) Get(id string) *Lobby {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lobbies[id]
}

// Delete closes all lobby conns before deleting it.
func (l *Lobbies) Delete(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if lobby := l.lobbies[id]; lobby != nil {
		lobby.Close()
	}

	delete(l.lobbies, id)
}
