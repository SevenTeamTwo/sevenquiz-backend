package quiz

import "sync"

type Lobbies struct {
	lobbies map[string]*Lobby
	mu      sync.Mutex
}

func (l *Lobbies) Register(id string, newLobby *Lobby) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.lobbies == nil {
		l.lobbies = map[string]*Lobby{}
	}
	l.lobbies[id] = newLobby
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
	if lobby != nil && lobby.NumConns() > 0 {
		lobby.CloseConns()
	}

	delete(l.lobbies, id)
}
