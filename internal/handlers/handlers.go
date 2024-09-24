package handlers

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sevenquiz-backend/api"
	"sevenquiz-backend/internal/config"
	apierrs "sevenquiz-backend/internal/errors"
	"sevenquiz-backend/internal/quiz"
	"sevenquiz-backend/internal/websocket"
	"time"
	"unicode/utf8"

	gws "github.com/gorilla/websocket"
)

// CreateLobbyHandler returns a handler capable of creating new lobbies
// and storing them in the lobbies container.
func CreateLobbyHandler(cfg config.Config, lobbies *quiz.Lobbies) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lobby, err := lobbies.Register(quiz.LobbyOptions{
			MaxPlayers: cfg.Lobby.MaxPlayers,
		})
		if err != nil {
			apierrs.HTTPErrorResponse(w, http.StatusInternalServerError, err, apierrs.InternalServerError())
		}

		res := api.CreateLobbyResponse{
			LobbyID: lobby.ID(),
		}
		if err := json.NewEncoder(w).Encode(res); err != nil {
			log.Println(err)
		}

		// Lobby idle timeout
		go func() {
			select {
			case <-lobby.Done():
				return
			case <-time.After(cfg.Lobby.RegisterTimeout):
				switch lobby.State() {
				case quiz.LobbyStateCreated, quiz.LobbyStateRegister:
					// TODO: broadcast to conns before ?
					lobbies.Delete(lobby.ID())
				}
			}
		}()
	}
}

// LobbyToAPIResponse converts a lobby to an API representation.
func LobbyToAPIResponse(lobby *quiz.Lobby) api.LobbyData {
	data := api.LobbyData{
		ID:         lobby.ID(),
		MaxPlayers: lobby.MaxPlayers(),
		PlayerList: lobby.GetPlayerList(),
		Created:    lobby.CreationDate().Format(time.RFC3339),
	}
	if owner := lobby.Owner(); owner != "" {
		data.Owner = &owner
	}
	return data
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

		state := lobby.State()
		if state == quiz.LobbyStateRegister && lobby.IsFull() { // Add delta of 1 to consider current conn.
			apierrs.HTTPErrorResponse(w, http.StatusForbidden, nil, apierrs.TooManyPlayersError(lobby.MaxPlayers()))
			return
		}
		// Transition to the registration state only after a first call to the handler.
		if state == quiz.LobbyStateCreated && lobby.NumConns() == 0 {
			lobby.SetState(quiz.LobbyStateRegister)
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// Upgrade already writes a status code and error message.
			log.Println(err)
			return
		}

		wsConn := websocket.NewConn(conn)
		defer handleDisconnect(lobbies, lobby, wsConn)

		handleRegister(cfg, lobby, wsConn)
	}
}

func handleDisconnect(lobbies *quiz.Lobbies, lobby *quiz.Lobby, conn *websocket.Conn) {
	conn.Close()

	switch lobby.State() {
	/*
		In the first stages we expect a first conn to be registered as owner.
		If there is none at defer execution, the lobby will keep waiting for
		one or ultimately be deleted by the lobby's register timeout.
		If there was one and other players are in lobby, the next player will
		be designated as owner. Otherwise the lobby is deleted.
	*/
	case quiz.LobbyStateCreated, quiz.LobbyStateRegister:
		// Capture client before deletion.
		cli, ok := lobby.GetPlayerByConn(conn)

		// Makes sure a player slot is freed and removed from list.
		lobby.DeletePlayerByConn(conn)

		if !ok || cli == nil {
			// Conn did not register, free a player slot.
			return
		}

		username := cli.Username()
		if err := lobby.BroadcastPlayerUpdate(username, "disconnect"); err != nil {
			log.Println(err)
		}

		if lobby.Owner() != username {
			// Conn was not owner, simply free the slot.
			return
		}

		players := lobby.GetPlayerList()

		// No other players in lobby and owner has left so discard lobby.
		if len(players) == 0 {
			lobbies.Delete(lobby.ID())
			return
		}

		newOwner := players[0]
		lobby.SetOwner(newOwner)

		if err := lobby.BroadcastPlayerUpdate(newOwner, "new owner"); err != nil {
			log.Println(err)
		}
	default:
		// TODO: next stages
		// Client's connect/disconnect/login/broadcast
	}
}

func handleRegister(cfg config.Config, lobby *quiz.Lobby, conn *websocket.Conn) {
	lobby.AddConn(conn)

	// Send banner on websocket upgrade with lobby details.
	handleLobbyRequest(lobby, conn)

	for {
		req := api.Request{}
		if err := conn.ReadJSON(&req); err != nil { // Blocks until next request
			apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("bad json"))
			return
		}

		switch req.Type {
		case api.RequestTypeLobby:
			handleLobbyRequest(lobby, conn)
		case api.RequestTypeRegister:
			handleRegisterRequest(cfg, lobby, conn, req.Data)
		default:
			apierrs.WebsocketErrorResponse(conn, nil, apierrs.InvalidRequestError("unknown request type"))
			continue
		}
	} // TODO: on start, transition to next phase with handleQuiz() with other requests handlers.
}

func handleLobbyRequest(lobby *quiz.Lobby, conn *websocket.Conn) {
	res := api.Response{
		Type: api.ResponseTypeLobby,
		Data: LobbyToAPIResponse(lobby),
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}
}

func handleRegisterRequest(_ config.Config, lobby *quiz.Lobby, conn *websocket.Conn, data any) {
	req, err := api.DecodeJSON[api.RegisterRequestData](data)
	if err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidRequestError("invalid register request"))
		return
	}

	// cancel register if user already logged in.
	if client, ok := lobby.GetPlayerByConn(conn); ok && client != nil {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.UserAlreadyRegisteredError())
		return
	}

	if err := validateUsername(req.Username); err != nil {
		apierrs.WebsocketErrorResponse(conn, err, apierrs.InvalidUsernameError(err.Error()))
		return
	}

	if _, _, exist := lobby.GetPlayer(req.Username); exist {
		apierrs.WebsocketErrorResponse(conn, nil, apierrs.UsernameAlreadyExistsError())
		return
	}

	client := lobby.AddPlayerWithConn(conn, req.Username)

	res := api.Response{
		Type: api.ResponseTypeRegister,
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}
	if err := lobby.BroadcastPlayerUpdate(client.Username(), "join"); err != nil {
		log.Println(err)
	}

	// Grant first user to join lobby owner permission.
	if lobby.Owner() == "" {
		lobby.SetOwner(req.Username)
		if err := lobby.BroadcastPlayerUpdate(req.Username, "new owner"); err != nil {
			log.Println(err)
		}
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
