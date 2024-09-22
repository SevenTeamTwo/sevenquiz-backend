package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sevenquiz-api/api"
	"sevenquiz-api/internal/config"
	apierrs "sevenquiz-api/internal/errors"
	"sevenquiz-api/internal/quiz"
	"sevenquiz-api/internal/websocket"
	"time"
	"unicode/utf8"

	gws "github.com/gorilla/websocket"
)

// CreateLobbyHandler returns a handler capable of creating new lobbies
// and storing them in the lobbies container.
func CreateLobbyHandler(cfg config.Config, lobbies *quiz.Lobbies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// A non-empty username will attribute a default owner and generate
		// a dedicated token before the lobby is joined. This is a way to
		// secure lobby's ownership for the creator.
		username := r.URL.Query().Get("username")
		if username != "" {
			if err := validateUsername(username); err != nil {
				apierrs.HTTPErrorResponse(w, http.StatusBadRequest, err, apierrs.InvalidUsernameError(err.Error()))
				return
			}
		}

		lobby, err := lobbies.Register(quiz.LobbyOptions{
			Owner:      username,
			MaxPlayers: cfg.Lobby.MaxPlayers,
		})
		if err != nil {
			apierrs.HTTPErrorResponse(w, http.StatusInternalServerError, err, apierrs.InternalServerError())
		}

		res := api.CreateLobbyResponse{
			LobbyID: lobby.ID(),
		}

		if username != "" {
			if res.Token, err = lobby.NewToken(cfg, username); err != nil {
				lobbies.Delete(lobby.ID())
				apierrs.HTTPErrorResponse(w, http.StatusInternalServerError, err, apierrs.InternalServerError())
				return
			}
			// Owner has not joined the lobby yet, register a nil conn in order
			// to retrieve it on login.
			lobby.NewClient(username, nil)
		}

		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Println(err)
		}

		// Lobby idle timeout
		go func() {
			<-time.After(cfg.Lobby.RegisterTimeout)
			if lobby.State() == quiz.LobbyStateCreated || lobby.State() == quiz.LobbyStateRegister {
				// TODO: broadcast to conns before ?
				lobbies.Delete(lobby.ID())
			}
		}()
	}
}

// LobbyToRoomResponse converts a lobby to an API representation.
func LobbyToRoomResponse(lobby *quiz.Lobby) api.RoomData {
	return api.RoomData{
		ID:         lobby.ID(),
		Owner:      lobby.Owner(),
		MaxPlayers: lobby.MaxPlayers(),
		PlayerList: lobby.GetPlayerList(),
	}
}

func handleRoom(lobby *quiz.Lobby, conn *websocket.Conn) {
	res := api.Response{
		Type: api.ResponseTypeRoom,
		Data: LobbyToRoomResponse(lobby),
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}
}

func handle(cfg config.Config, lobby *quiz.Lobby, conn *websocket.Conn) {
	lobby.NewUnregisteredClient(conn)

	defer func() {
		conn.Close()
		if cli, ok := lobby.GetClientByConn(conn); ok && cli != nil {
			cli.Disconnect()
			if err := lobby.BroadcastLobbyUpdate(cli.Username(), "disconnect"); err != nil {
				log.Println(err)
			}
		} else {
			// Release unregistered conn slot to not reach max players
			// without any players...
			lobby.DeleteConn(conn)
		}
	}()

	handleRoom(lobby, conn)

	for {
		req := api.Request{}
		if err := conn.ReadJSON(&req); err != nil { // Blocks until next request
			apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("bad json"))
			return
		}

		switch req.Type {
		case api.RequestTypeRoom:
			handleRoom(lobby, conn)
		case api.RequestTypeRegister:
			handleRegister(cfg, lobby, conn, req.Data)
		case api.RequestTypeLogin:
			handleLogin(cfg, lobby, conn, req.Data)
		default:
			apierrs.WebsocketErrorResponse(conn, nil, apierrs.InvalidRequestError("unknown request type"))
			continue
		}
	} // TODO: on start, goto next phase
}

// LobbyHandler returns a new lobby handler and will run a complete
// quiz game upon it's completion.
func LobbyHandler(cfg config.Config, lobbies *quiz.Lobbies, upgrader gws.Upgrader) http.HandlerFunc {
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

		if lobby.State() == quiz.LobbyStateRegister && lobby.IsFull() {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, nil, apierrs.TooManyPlayersError(lobby.MaxPlayers()))
			return
		}
		// Transition to the registration state only after a first call to the handler.
		if lobby.NumConns() == 0 {
			lobby.SetState(quiz.LobbyStateRegister)
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already writes a status code and error message.
			log.Println(err)
			return
		}
		wsConn := websocket.NewConn(conn)

		handle(cfg, lobby, wsConn)
	}
}

func handleRegister(cfg config.Config, lobby *quiz.Lobby, conn *websocket.Conn, data any) {
	req, err := api.DecodeJSON[api.RegisterRequestData](data)
	if err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("invalid register request"))
		return
	}

	// cancel register if user already logged in.
	if client, ok := lobby.GetClientByConn(conn); ok && client != nil {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.UserAlreadyRegisteredError())
		return
	}

	if err := validateUsername(req.Username); err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidUsernameError(err.Error()))
		return
	}

	if _, _, exist := lobby.GetClient(req.Username); exist {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.UsernameAlreadyExistsError())
		return
	}

	client := lobby.NewClient(req.Username, conn)

	// Grant first user to join lobby owner permission if none predefined.
	if lobby.Owner() == "" {
		lobby.SetOwner(req.Username)
	}

	token, err := lobby.NewToken(cfg, req.Username)
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

	if err := lobby.BroadcastLobbyUpdate(client.Username(), "join"); err != nil {
		log.Println(err)
	}
}

func handleLogin(cfg config.Config, lobby *quiz.Lobby, conn *websocket.Conn, data any) {
	req, err := api.DecodeJSON[api.LoginRequestData](data)
	if err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("invalid login request"))
		return
	}

	claims, err := lobby.CheckToken(cfg, req.Token)
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

	oldConn, replaced := lobby.ReplaceClientConn(username, conn)
	if !replaced {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.ClientRestituteError("no client to resitute"))
		return
	}

	res := api.Response{
		Type:    api.ResponseTypeLogin,
		Message: "login successful",
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}

	action := "reconnect"
	// Lobby owner's conn is assigned a nil conn before login.
	if username == lobby.Owner() && oldConn == nil {
		action = "join"
	}
	if err := lobby.BroadcastLobbyUpdate(username, action); err != nil {
		log.Println(err)
	}
}

func validateUsername(username string) error {
	count := utf8.RuneCountInString(username)
	if count < 3 {
		return errors.New("username too short")
	}
	if count > 25 {
		return errors.New("username too long")
	}
	return nil
}
