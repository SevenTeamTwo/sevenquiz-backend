package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
)

type client struct {
	// Username is unique and used to define the lobby owner.
	Username string

	// Score represents the user's quiz score
	Score float64
}

type lobbyState int

const (
	lobbyStateCreated lobbyState = iota
	lobbyStateRegister
	lobbyStateQuiz
	lobbyStateResponses
	lobbyStateEnded
)

type lobby struct {
	ID         string    `json:"id"`
	Created    time.Time `json:"created"`
	Owner      string    `json:"owner"`
	MaxPlayers uint64    `json:"maxPlayers"`
	PlayerList []string  `json:"playerList"`

	// tokenValidity invalidates an access token if the "tokenValidity" claim
	// doesn't match. Since lobby ids are short-sized, it prevents previous
	// lobby owner/players from accessing a newly created lobby with the old token.
	tokenValidity string
	mu            sync.Mutex
	state         lobbyState
	clients       map[*websocket.Conn]client // registered clients
	numConns      atomic.Uint64              // number of websocket conns
}

var (
	lobbies   = map[string]*lobby{}
	lobbiesMu sync.Mutex

	defaultMaxPlayers uint64 = 25
)

type jsonLobby lobby

func (l *lobby) MarshalJSON() ([]byte, error) {
	lobby := jsonLobby{
		ID:         l.ID,
		Created:    l.Created,
		Owner:      l.Owner,
		MaxPlayers: l.MaxPlayers,
		PlayerList: make([]string, 0, len(l.clients)),
	}
	for _, conn := range l.clients {
		lobby.PlayerList = append(lobby.PlayerList, conn.Username)
	}
	return json.Marshal(&lobby)
}

func (l *lobby) IncConn() {
	l.numConns.Add(1)
}

func (l *lobby) DecConn() {
	l.numConns.Add(^uint64(0))
}

func (l *lobby) setState(state lobbyState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

func (l *lobby) assignConn(conn *websocket.Conn, client client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.clients[conn] = client
}

func (l *lobby) deleteConn(conn *websocket.Conn) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.clients, conn)
}

func addLobby(id string, lobby *lobby) {
	lobbiesMu.Lock()
	defer lobbiesMu.Unlock()
	lobbies[id] = lobby
}

func checkUsername(username string) error {
	if username == "" {
		return errors.New("missing username")
	}
	if utf8.RuneCountInString(username) > 25 {
		return errors.New("username too long")
	}
	return nil
}

func (l *lobby) checkToken(token string) (claims jwt.MapClaims, err error) {
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return jwtSecret, nil
	})
	if err != nil {
		return nil, err
	}

	claims, ok := jwtToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("could not assert jwt claims")
	}

	tokenValidity, ok := claims["tokenValidity"]
	if !ok {
		return nil, errors.New("token has no tokenValidity claim")
	}
	tokenValidityStr, ok := tokenValidity.(string)
	if !ok {
		return nil, errors.New("could not assert token validity to string")
	}
	if tokenValidityStr != l.tokenValidity {
		return nil, errors.New("token does not match token validity")
	}

	return claims, nil
}
