package quiz

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sevenquiz-api/api"
	"sevenquiz-api/internal/config"
	apierrs "sevenquiz-api/internal/errors"
	"sevenquiz-api/internal/websocket"
	"time"
	"unicode/utf8"

	"github.com/go-viper/mapstructure/v2"
	gws "github.com/gorilla/websocket"

	"github.com/lithammer/shortuuid/v3"
)

func CreateLobbyHandler(cfg config.Config, lobbies *Lobbies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		username := r.URL.Query().Get("username")
		if err := checkUsername(username); err != nil {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, err, apierrs.InvalidUsernameError(err.Error()))
			return
		}

		var newLobby *Lobby

		for {
			lobbyID := shortuuid.New()
			if len(lobbyID) < 5 {
				err := errors.New("generated id too short: " + lobbyID)
				apierrs.HTTPErrorResponse(w, http.StatusInternalServerError, err, apierrs.InternalServerError())

				return
			}

			lobbyID = lobbyID[:5]

			if l := lobbies.Get(lobbyID); l == nil {
				newLobby = &Lobby{
					ID:         lobbyID,
					Owner:      username,
					MaxPlayers: cfg.Lobby.MaxPlayers,
				}
				lobbies.Register(newLobby.ID, newLobby)

				break
			}
		}

		token, err := newLobby.NewToken(cfg, username)
		if err != nil {
			lobbies.Delete(newLobby.ID)
			apierrs.HTTPErrorResponse(w, http.StatusInternalServerError, err, apierrs.InternalServerError())

			return
		}

		// Owner has not upgraded to websocket yet, register a nil conn
		// in order to retrieve it on login.
		newLobby.AssignConn(&Client{Username: username}, nil)

		res := api.CreateLobbyResponse{
			LobbyID: newLobby.ID,
			Token:   token,
		}

		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Println(err)
		}

		// Lobby timeout
		go func() {
			<-time.After(cfg.Lobby.RegisterTimeout)
			if newLobby.state == LobbyStateCreated || newLobby.state == LobbyStateRegister {
				// TODO: broadcast to conns before ?
				lobbies.Delete(newLobby.ID)
			}
		}()
	}
}

func (l *Lobby) LobbyToRoomResponse() api.RoomData {
	return api.RoomData{
		ID:         l.ID,
		Owner:      l.Owner,
		MaxPlayers: l.MaxPlayers,
		PlayerList: l.GetPlayerList(),
	}
}

func (l *Lobby) handleRoom(conn *websocket.Conn) {
	res := api.Response{
		Type: api.ResponseTypeRoom,
		Data: l.LobbyToRoomResponse(),
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}
}

func (l *Lobby) handle(cfg config.Config, conn *websocket.Conn) {
	l.AssignConn(nil, conn)

	defer func() {
		if c := l.GetClientFromConn(conn); c != nil {
			c.Disconnect()
		}
		conn.Close()
	}()

	l.handleRoom(conn)

	for {
		req := api.Request{}
		if err := conn.ReadJSON(&req); err != nil { // Blocks until next request
			defer conn.Close()

			apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("bad json"))

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
		case api.RequestTypeRoom:
			l.handleRoom(conn)
		case api.RequestTypeRegister:
			l.handleRegister(cfg, conn, req.Data)
		case api.RequestTypeLogin:
			l.handleLogin(cfg, conn, req.Data)
		default:
			apierrs.WebsocketErrorResponse(conn, nil, apierrs.InvalidRequestError("unknown request type"))
			continue
		}
	} // TODO: on start, goto next phase
}

func LobbyHandler(cfg config.Config, lobbies *Lobbies, upgrader gws.Upgrader) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, nil, apierrs.MissingURLQueryError("id"))
			return
		}

		lobby := lobbies.Get(id)
		if lobby == nil {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, nil, apierrs.LobbyNotFoundError())
			return
		}
		if lobby.state == LobbyStateRegister && lobby.NumConns() > lobby.MaxPlayers {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, nil, apierrs.TooManyPlayersError(lobby.MaxPlayers))
			return
		}
		// Transition to the registration state only after a first call to the handler.
		if lobby.NumConns() == 0 {
			lobby.SetState(LobbyStateRegister)
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already writes a status code and error message.
			log.Println(err)
			return
		}
		wsConn := websocket.NewConn(conn)

		lobby.handle(cfg, wsConn)
	}
}

func (l *Lobby) broadcast(v any) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	errs := []error{}
	for conn := range l.clients {
		if conn == nil {
			continue
		}
		if err := conn.WriteJSON(v); err != nil {
			errs = append(errs, err)
		}
	}

	return errors.Join(errs...)
}

func (l *Lobby) broadcastPlayerUpdate(username, action string) error {
	res := api.Response{
		Type: api.ResponseTypeLobbyUpdate,
		Data: api.LobbyUpdateResponseData{
			Username: username,
			Action:   action,
		},
	}
	return l.broadcast(res)
}

func (l *Lobby) handleRegister(cfg config.Config, conn *websocket.Conn, reqData any) {
	reqDataMap, ok := reqData.(map[string]any)
	if !ok {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.InvalidRequestError("invalid register request"))
		return
	}
	data := api.RegisterRequestData{}
	if err := mapstructure.Decode(reqDataMap, &data); err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("invalid register request"))
		return
	}

	// cancel register if user already logged in.
	if client, ok := l.clients[conn]; ok && client != nil {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.UserAlreadyRegisteredError())
		return
	}

	if err := checkUsername(data.Username); err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidUsernameError(err.Error()))
		return
	}

	for _, client := range l.clients {
		if client == nil {
			continue
		}
		if client.Username == data.Username {
			apierrs.WebsocketErrorResponse(conn, nil, apierrs.UsernameAlreadyExistsError())
			return
		}
	}

	newClient := &Client{Username: data.Username, alive: true}
	l.AssignConn(newClient, conn)

	token, err := l.NewToken(cfg, data.Username)
	if err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.UsernameAlreadyExistsError())
		return
	}

	res := api.Response{
		Type: api.ResponseTypeRegister,
		Data: api.RegisterResponseData{
			Token: token,
		},
	}

	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}

	if err := l.broadcastPlayerUpdate(newClient.Username, "join"); err != nil {
		log.Println(err)
	}
}

func (l *Lobby) handleLogin(cfg config.Config, conn *websocket.Conn, reqData any) {
	reqDataMap, ok := reqData.(map[string]any)
	if !ok {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.InvalidRequestError("invalid login request"))
		return
	}
	data := api.LoginRequestData{}
	if err := mapstructure.Decode(reqDataMap, &data); err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("invalid login request"))
		return
	}

	claims, err := l.CheckToken(cfg, data.Token)
	if err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidTokenError())
		return
	}

	var username string

	usernameClaim, ok := claims["username"]
	if ok {
		username, ok = usernameClaim.(string)
	}
	if !ok || username == "" {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.InvalidTokenClaimError("username"))
		return
	}

	_, client, ok := l.GetClient(username)
	if !ok {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.ClientRestituteError("no client to resitute"))
		return
	}

	oldConn, _ := l.ReplaceConn(client, conn)

	res := api.Response{
		Type:    api.ResponseTypeLogin,
		Message: "login successful",
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}

	action := "reconnect"
	// Lobby owner's conn is assigned a nil conn before login.
	if client.Username == l.Owner && oldConn == nil {
		action = "join"
	}

	client.Connect()

	if err := l.broadcastPlayerUpdate(client.Username, action); err != nil {
		log.Println(err)
	}
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
