package quiz

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
)

type Client struct {
	// Username is unique and used to define the lobby owner.
	Username string

	// Score represents the user's quiz score
	Score float64

	// hasAlreadyLoggedIn specifies if the client has already joined
	// the lobby once.
	hasAlreadyLoggedIn bool
}

type lobbyState int

const (
	LobbyStateCreated lobbyState = iota
	LobbyStateRegister
	LobbyStateQuiz
	LobbyStateResponses
	LobbyStateEnded
)

// TODO: better way to instantiate a lobby since exposed token secret.
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
	TokenSecret   []byte `json:"-"`

	// clients represents all the active websockets in a lobby.
	// A client != nil means a conn has registered.
	clients map[*websocket.Conn]*Client
	mu      sync.Mutex
	state   lobbyState
}

func (l *Lobby) NumConns() int {
	return len(l.clients)
}

func (l *Lobby) CloseConns() {
	for conn := range l.clients {
		if conn != nil {
			conn.Close()
		}
	}
}

func (l *Lobby) RangeClients(f func(conn *websocket.Conn, cli *Client) bool) {
	// TODO: Is it worth it ? GetClient instead ?
	for conn, cli := range l.clients {
		if ok := f(conn, cli); !ok {
			return
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
		PlayerList: make([]string, 0, l.NumConns()),
	}
	for _, client := range l.clients {
		if client == nil {
			continue
		}
		lobby.PlayerList = append(lobby.PlayerList, client.Username)
	}
	return json.Marshal(&lobby)
}

func (l *Lobby) GetClient(username string) *Client {
	for _, client := range l.clients {
		if client == nil {
			continue
		}
		if client.Username == username {
			return client
		}
	}
	return nil
}

func (l *Lobby) SetState(state lobbyState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

func (l *Lobby) AssignConn(conn *websocket.Conn, client *Client) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.clients == nil {
		l.clients = make(map[*websocket.Conn]*Client)
	}
	l.clients[conn] = client
}

func (l *Lobby) ReplaceClientConn(client *Client, newConn *websocket.Conn) {
	if client == nil {
		return
	}

	l.mu.Lock()
	defer l.mu.Unlock()

	for conn, cli := range l.clients {
		if cli.Username == client.Username {
			conn.Close()
			delete(l.clients, conn)
		}
	}

	l.clients[newConn] = client
}

func (l *Lobby) DeleteConn(conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if conn != nil {
		conn.Close()
	}

	delete(l.clients, conn)
}

func (l *Lobby) NewToken(username string) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"lobbyId":       l.ID,
		"tokenValidity": l.TokenValidity,
		"username":      username,
	})
	return token.SignedString(l.TokenSecret)
}

func (l *Lobby) CheckToken(token string) (claims jwt.MapClaims, err error) {
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return l.TokenSecret, nil
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
