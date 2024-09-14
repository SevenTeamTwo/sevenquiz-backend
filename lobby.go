package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid/v3"
)

type client struct {
	// Username is unique and used to define the lobby owner.
	Username string
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
	MaxPlayers int       `json:"max_players"`

	// tokenValidity invalidates an access token if the "token_validity" claim
	// doesn't match. Since lobby ids are short-sized, it prevents previous
	// lobby owner/players from accessing a newly created lobby with the old token.
	tokenValidity string
	mu            sync.Mutex
	state         lobbyState
	clients       map[*websocket.Conn]client // registered clients
	numConns      int                        // number of websocket conns
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

var lobbies = map[string]*lobby{}
var lobbiesMu sync.Mutex

func addLobby(id string, lobby *lobby) {
	lobbiesMu.Lock()
	defer lobbiesMu.Unlock()

	lobbies[id] = lobby
}

type createLobbyResponse struct {
	LobbyID string `json:"id"`
	Token   string `json:"token"`
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

func newCreateLobbyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		if err := checkUsername(username); err != nil {
			httpErrorResponse(w, http.StatusBadRequest, err, newInvalidUsernameError(err.Error()))
			return
		}

		var lobbyID, tokenValidity string

		for {
			lobbyID = shortuuid.New()
			if len(lobbyID) < 5 {
				err := errors.New("generated id too short: " + lobbyID)
				httpErrorResponse(w, http.StatusInternalServerError, err, newInternalServerError())

				return
			}

			lobbyID = lobbyID[:5]
			tokenValidity = shortuuid.New()

			if _, exist := lobbies[lobbyID]; !exist {
				addLobby(lobbyID, &lobby{
					ID:            lobbyID,
					Created:       time.Now(),
					Owner:         username,
					MaxPlayers:    25,
					tokenValidity: tokenValidity,
					state:         lobbyStateCreated,
					clients:       map[*websocket.Conn]client{},
				})

				break
			}
		}

		// TODO: invalidate token on lobby deletion.
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"lobby_id":       lobbyID,
			"token_validity": tokenValidity,
			"username":       username,
		})

		tokenStr, err := token.SignedString(jwtSecret)
		if err != nil {
			delete(lobbies, lobbyID)
			httpErrorResponse(w, http.StatusInternalServerError, err, newInternalServerError())

			return
		}

		// Owner has not upgraded to websocket yet, register a nil conn
		// in order to retrieve it on login.
		lobbies[lobbyID].clients[nil] = client{Username: username}

		res := createLobbyResponse{
			LobbyID: lobbyID,
			Token:   tokenStr,
		}

		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Println(err)
		}
	}
}

func newLobbyHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpErrorResponse(w, http.StatusBadRequest, nil, newMissingURLQueryError("id"))
			return
		}

		lobby, exist := lobbies[id]
		if !exist {
			httpErrorResponse(w, http.StatusBadRequest, nil, newLobbyNotFoundError())
			return
		}

		if lobby.state == lobbyStateRegister && lobby.numConns > lobby.MaxPlayers {
			httpErrorResponse(w, http.StatusBadRequest, nil, newTooManyPlayersError(lobby.MaxPlayers))
			return
		}

		conn, err := defaultUpgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already writes a status code and error message.
			log.Println(err)

			return
		}

		lobby.numConns++
		defer func() {
			if lobby.numConns > 0 {
				lobby.numConns--
			}
		}()

		// Transition to the register state only after a first
		// call to the handler.
		if len(lobby.clients) == 0 {
			lobby.setState(lobbyStateRegister)
		}

		for {
			req := apiRequest{}

			// Block until next request
			if err := conn.ReadJSON(&req); err != nil {
				websocketErrorResponse(conn, err, newInvalidRequestError("bad json"))
				conn.Close()

				return
			}

			switch req.Type {
			case requestTypeRegister:
				data := registerRequestData{}
				if err := unmarshalAny(req.Data, &data); err != nil {
					websocketErrorResponse(conn, err, newInvalidRequestError("invalid register request"))
					return
				}

				lobby.handleRegister(conn, data)
			case requestTypeLogin:
				data := loginRequestData{}
				if err := unmarshalAny(req.Data, &data); err != nil {
					websocketErrorResponse(conn, err, newInvalidRequestError("invalid login request"))
					return
				}

				lobby.handleLogin(conn, data)
			default:
				websocketErrorResponse(conn, nil, newInvalidRequestError("unknown request type"))
				continue
			}
		} // TODO: on start, goto next phase
	}
}

type registerRequestData struct {
	Username string `json:"username"`
}

type registerResponseData struct {
	Token string `json:"token"`
}

func (l *lobby) handleRegister(conn *websocket.Conn, data registerRequestData) {
	// cancel register if user already logged in.
	if _, ok := l.clients[conn]; ok {
		websocketErrorResponse(conn, nil, newUserAlreadyRegisteredError())
		return
	}

	if err := checkUsername(data.Username); err != nil {
		websocketErrorResponse(conn, err, newInvalidUsernameError(err.Error()))
		return
	}

	for _, client := range l.clients {
		if client.Username == data.Username {
			websocketErrorResponse(conn, nil, newUsernameAlreadyExistsError())
			return
		}
	}

	c := client{Username: data.Username}
	l.assignConn(conn, c)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"lobby_id":       l.ID,
		"token_validity": l.tokenValidity,
		"username":       data.Username,
	})

	tokenStr, err := token.SignedString(jwtSecret)
	if err != nil {
		websocketErrorResponse(conn, err, newUsernameAlreadyExistsError())
		return
	}

	res := apiResponse{
		Type: responseTypeRegister,
		Data: registerResponseData{
			Token: tokenStr,
		},
	}

	conn.WriteJSON(res)
}

type loginRequestData struct {
	Token string `json:"token"`
}

func (l *lobby) handleLogin(conn *websocket.Conn, data loginRequestData) {
	claims, err := l.checkToken(data.Token)
	if err != nil {
		websocketErrorResponse(conn, err, newInvalidTokenError())
		return
	}

	var username string

	usernameClaim, ok := claims["username"]
	if ok {
		username, ok = usernameClaim.(string)
	}

	if !ok || username == "" {
		websocketErrorResponse(conn, nil, newInvalidTokenClaimError("username"))
		return
	}

	var (
		oldConn   *websocket.Conn
		client    client
		restitute bool
	)

	for oldConn, client = range l.clients {
		if client.Username == username {
			restitute = true

			break
		}
	}

	if !restitute {
		websocketErrorResponse(conn, nil, newClientRestituteError("no client to resitute"))
		return
	}

	// Close old connection to avoid network leaks.
	if oldConn != nil {
		oldConn.Close()
	}

	l.deleteConn(oldConn)
	l.assignConn(conn, client)

	res := apiResponse{
		Type:    responseTypeLogin,
		Message: "login successful",
	}

	conn.WriteJSON(res)
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

	tokenValidity, ok := claims["token_validity"]
	if !ok {
		return nil, errors.New("token has no token_validity claim")
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

func unmarshalAny(data map[string]any, v any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}

	return json.Unmarshal(b, v)
}
