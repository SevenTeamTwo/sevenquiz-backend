package quiz

import (
	"errors"
	"sevenquiz-api/internal/websocket"
	"sync"
	"time"

	"github.com/lithammer/shortuuid/v3"
)

type Lobbies struct {
	lobbies map[string]*Lobby
	mu      sync.Mutex
}

var errNoLobbySlotAvailable = errors.New("no lobby slot available")

type LobbyOptions struct {
	// Owner represents the lobby's owner.
	// This priviledged user has the rights to start a lobby
	// and credit points at the end of the quiz.
	// Empty value will grant the first user to login owner privileges.
	Owner string

	// MaxPlayers defines the maximum amount of players
	// allowed to join a lobby. This limit is reached even
	// with a lobby filled with unregistered users.
	// Default is set to 25. Negative value means no limit.
	MaxPlayers int
}

func (l *Lobbies) Register(opts LobbyOptions) (*Lobby, error) {
	if opts.MaxPlayers == 0 {
		opts.MaxPlayers = 25
	}

	lobby := &Lobby{
		id:            newLobbyID(),
		owner:         opts.Owner,
		maxPlayers:    opts.MaxPlayers,
		tokenValidity: shortuuid.New(),
		clients:       map[*websocket.Conn]*Client{},
		created:       time.Now(),
		state:         LobbyStateCreated,
	}

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

func (l *Lobbies) Get(id string) *Lobby {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.lobbies[id]
}

func (l *Lobbies) Delete(id string) {
	l.mu.Lock()
	defer l.mu.Unlock()

	lobby := l.lobbies[id]
	if lobby != nil {
		defer lobby.CloseConns()
	}

	delete(l.lobbies, id)
}
