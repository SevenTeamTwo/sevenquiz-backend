package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid/v3"
)

type createLobbyResponse struct {
	LobbyID string `json:"id"`
	Token   string `json:"token"`
}

func newCreateLobbyHandler(lobbies *lobbies, maxPlayers int, lobbyTimeout time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		if err := checkUsername(username); err != nil {
			httpErrorResponse(w, http.StatusBadRequest, err, newInvalidUsernameError(err.Error()))
			return
		}

		var newLobby *lobby

		for {
			lobbyID := shortuuid.New()
			if len(lobbyID) < 5 {
				err := errors.New("generated id too short: " + lobbyID)
				httpErrorResponse(w, http.StatusInternalServerError, err, newInternalServerError())

				return
			}

			lobbyID = lobbyID[:5]
			tokenValidity := shortuuid.New()

			if l := lobbies.get(lobbyID); l == nil {
				newLobby = &lobby{
					ID:            lobbyID,
					Created:       time.Now(),
					Owner:         username,
					MaxPlayers:    maxPlayers,
					tokenValidity: tokenValidity,
					state:         lobbyStateCreated,
					clients:       map[*websocket.Conn]*client{},
				}
				lobbies.register(newLobby.ID, newLobby)

				break
			}
		}

		// TODO: invalidate token on lobby deletion.
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"lobbyId":       newLobby.ID,
			"tokenValidity": newLobby.tokenValidity,
			"username":      username,
		})
		tokenStr, err := token.SignedString(jwtSecret)
		if err != nil {
			lobbies.delete(newLobby.ID)
			httpErrorResponse(w, http.StatusInternalServerError, err, newInternalServerError())

			return
		}

		// Owner has not upgraded to websocket yet, register a nil conn
		// in order to retrieve it on login.
		newLobby.assignConn(nil, &client{Username: username})

		res := createLobbyResponse{
			LobbyID: newLobby.ID,
			Token:   tokenStr,
		}

		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Println(err)
		}

		// Lobby timeout
		go func() {
			<-time.After(lobbyTimeout)
			if newLobby.state == lobbyStateCreated || newLobby.state == lobbyStateRegister {
				for conn := range newLobby.clients {
					// TODO: broadcast to conns before ?
					if conn == nil {
						continue
					}
					conn.Close()
				}
				lobbies.delete(newLobby.ID)
			}
		}()
	}
}

func (l *lobby) banner(conn *websocket.Conn) error {
	if conn == nil {
		return errors.New("nil websocket")
	}
	res := apiResponse{
		Type: responseRoom,
		Data: l,
	}
	return conn.WriteJSON(res)
}

func (l *lobby) handleStepRegister(conn *websocket.Conn) {
	for {
		// Blocks until next request
		req := apiRequest{}
		if err := conn.ReadJSON(&req); err != nil {
			defer conn.Close()
			websocketErrorResponse(conn, err, newInvalidRequestError("bad json"))

			disconnectClient, exist := l.clients[conn]
			if !exist || disconnectClient == nil {
				return
			}
			err = l.broadcastPlayerUpdate(disconnectClient.Username, "disconnect")
			if err != nil {
				log.Println(err)
			}

			return
		}

		switch req.Type {
		case requestRoom:
			if err := l.banner(conn); err != nil {
				log.Println(err)
				return
			}
		case requestTypeRegister:
			l.handleRegister(conn, req.Data)
		case requestTypeLogin:
			l.handleLogin(conn, req.Data)
		default:
			websocketErrorResponse(conn, nil, newInvalidRequestError("unknown request type"))
			continue
		}
	} // TODO: on start, goto next phase
}

func newLobbyHandler(lobbies *lobbies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			httpErrorResponse(w, http.StatusBadRequest, nil, newMissingURLQueryError("id"))
			return
		}

		lobby := lobbies.get(id)
		if lobby == nil {
			httpErrorResponse(w, http.StatusBadRequest, nil, newLobbyNotFoundError())
			return
		}
		if lobby.state == lobbyStateRegister && len(lobby.clients) > lobby.MaxPlayers {
			httpErrorResponse(w, http.StatusBadRequest, nil, newTooManyPlayersError(lobby.MaxPlayers))
			return
		}
		// Transition to the registration state only after a first call to the handler.
		if len(lobby.clients) == 0 {
			lobby.setState(lobbyStateRegister)
		}

		conn, err := defaultUpgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already writes a status code and error message.
			log.Println(err)
			return
		}
		lobby.assignConn(conn, nil)
		defer lobby.deleteConn(conn)

		// Send banner to conn with current lobby info.
		if err := lobby.banner(conn); err != nil {
			log.Println(err)
			conn.Close()
			return
		}

		lobby.handleStepRegister(conn)
	}
}

type registerRequestData struct {
	Username string `json:"username"`
}

type registerResponseData struct {
	Token string `json:"token"`
}

type lobbyUpdateResponseData struct {
	Username string `json:"username,omitempty"`
	Action   string `json:"action"`
}

func (l *lobby) broadcast(v any) error {
	errs := []error{}
	for conn := range l.clients {
		if err := conn.WriteJSON(v); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (l *lobby) broadcastPlayerUpdate(username, action string) error {
	res := apiResponse{
		Type: responseLobbyUpdate,
		Data: lobbyUpdateResponseData{
			Username: username,
			Action:   action,
		},
	}
	return l.broadcast(res)
}

func (l *lobby) handleRegister(conn *websocket.Conn, rawJSONData json.RawMessage) {
	data := registerRequestData{}
	if err := json.Unmarshal(rawJSONData, &data); err != nil {
		websocketErrorResponse(conn, err, newInvalidRequestError("invalid register request"))
		return
	}

	// cancel register if user already logged in.
	if client, ok := l.clients[conn]; ok && client != nil {
		websocketErrorResponse(conn, nil, newUserAlreadyRegisteredError())
		return
	}

	if err := checkUsername(data.Username); err != nil {
		websocketErrorResponse(conn, err, newInvalidUsernameError(err.Error()))
		return
	}

	for _, client := range l.clients {
		if client == nil {
			continue
		}
		if client.Username == data.Username {
			websocketErrorResponse(conn, nil, newUsernameAlreadyExistsError())
			return
		}
	}

	newClient := &client{Username: data.Username}
	l.assignConn(conn, newClient)

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"lobbyId":       l.ID,
		"tokenValidity": l.tokenValidity,
		"username":      data.Username,
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

	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}

	if err := l.broadcastPlayerUpdate(newClient.Username, "join"); err != nil {
		log.Println(err)
	}
}

type loginRequestData struct {
	Token string `json:"token"`
}

func (l *lobby) handleLogin(conn *websocket.Conn, rawJSONData json.RawMessage) {
	data := loginRequestData{}
	if err := json.Unmarshal(rawJSONData, &data); err != nil {
		websocketErrorResponse(conn, err, newInvalidRequestError("invalid login request"))
		return
	}

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
		client    *client
		restitute bool
	)

	for oldConn, client = range l.clients {
		if client == nil {
			continue
		}
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

	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}

	if err := l.broadcastPlayerUpdate(client.Username, "reconnect"); err != nil {
		log.Println(err)
	}
}
