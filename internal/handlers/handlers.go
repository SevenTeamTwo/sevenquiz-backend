package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"sevenquiz-backend/api"
	"sevenquiz-backend/internal/config"
	apierrs "sevenquiz-backend/internal/errors"
	"sevenquiz-backend/internal/quiz"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// CreateLobbyHandler returns a handler capable of creating new lobbies
// and storing them in the lobbies container.
func CreateLobbyHandler(cfg config.Config, lobbies *quiz.Lobbies, quizzes fs.FS) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lobby, err := lobbies.Register(quiz.LobbyOptions{
			MaxPlayers: cfg.Lobby.MaxPlayers,
			Quizzes:    quizzes,
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
func LobbyToAPIResponse(lobby *quiz.Lobby) (api.LobbyData, error) {
	quizzes, err := lobby.ListQuizzes()
	if err != nil {
		return api.LobbyData{}, err
	}
	data := api.LobbyData{
		ID:          lobby.ID(),
		MaxPlayers:  lobby.MaxPlayers(),
		PlayerList:  lobby.GetPlayerList(),
		Created:     lobby.CreationDate().Format(time.RFC3339),
		Quizzes:     quizzes,
		CurrentQuiz: lobby.Quiz(),
	}
	if owner := lobby.Owner(); owner != "" {
		data.Owner = &owner
	}
	return data, nil
}

// LobbyHandler returns a new lobby handler and will run a complete
// quiz game upon it's completion.
func LobbyHandler(cfg config.Config, lobbies *quiz.Lobbies, acceptOpts websocket.AcceptOptions) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		if id == "" {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, nil, apierrs.MissingURLQueryError("id"))
			return
		}

		lobby, ok := lobbies.Get(id)
		if !ok || lobby == nil {
			apierrs.HTTPErrorResponse(w, http.StatusBadRequest, nil, apierrs.LobbyNotFoundError())
			return
		}

		state := lobby.State()
		if state == quiz.LobbyStateRegister && lobby.IsFull() {
			apierrs.HTTPErrorResponse(w, http.StatusForbidden, nil, apierrs.TooManyPlayersError(lobby.MaxPlayers()))
			return
		}
		// Transition to the registration state only after a first call to the handler.
		if state == quiz.LobbyStateCreated && lobby.NumConns() == 0 {
			lobby.SetState(quiz.LobbyStateRegister)
		}

		conn, err := websocket.Accept(w, r, &acceptOpts)
		if err != nil {
			// Accept already writes a status code and error message.
			log.Println(err)
			return
		}
		conn.SetReadLimit(cfg.Lobby.WebsocketReadLimit)

		ctx := r.Context()
		go ping(ctx, conn, 5*time.Second) // Detect timed out connection.
		defer handleDisconnect(ctx, lobbies, lobby, conn)

		switch lobby.State() {
		case quiz.LobbyStateCreated, quiz.LobbyStateRegister:
			handleRegister(ctx, lobby, conn)
		}
	}
}

func ping(ctx context.Context, conn *websocket.Conn, interval time.Duration) {
	for {
		select {
		case <-time.Tick(interval):
			if conn == nil {
				return
			}
			timeoutCtx, cancel := context.WithTimeout(ctx, time.Second*10)
			if err := conn.Ping(timeoutCtx); err != nil {
				log.Println("ping failed, closing conn")
				conn.CloseNow()
				cancel()
				return
			}
			cancel()
		case <-ctx.Done():
			return
		}
	}
}

func handleDisconnect(ctx context.Context, lobbies *quiz.Lobbies, lobby *quiz.Lobby, conn *websocket.Conn) {
	conn.CloseNow()

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

		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		username := cli.Username()
		if err := lobby.BroadcastPlayerUpdate(timeoutCtx, username, "disconnect"); err != nil {
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

		if err := lobby.BroadcastPlayerUpdate(timeoutCtx, newOwner, "new owner"); err != nil {
			log.Println(err)
		}
	default:
		// TODO: next stages
		// Client's connect/disconnect/login/broadcast
	}
}

func handleRegister(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn) {
	lobby.AddConn(conn)

	// Send banner on websocket upgrade with lobby details.
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	handleLobbyRequest(timeoutCtx, lobby, conn)
	cancel()

	for {
		req := &api.Request{}
		err := wsjson.Read(ctx, conn, &req)
		if err != nil {
			if websocket.CloseStatus(err) == -1 { // -1 is considered as an err unrelated to closing.
				timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				apierrs.WebsocketErrorResponse(timeoutCtx, conn, err, apierrs.InvalidRequestError("could not read websocket frame"))
			} else {
				log.Println(err)
			}
			return
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)

		switch req.Type {
		case api.RequestTypeLobby:
			handleLobbyRequest(timeoutCtx, lobby, conn)
		case api.RequestTypeRegister:
			handleRegisterRequest(timeoutCtx, lobby, conn, req.Data)
		case api.RequestTypeKick:
			handleKickRequest(timeoutCtx, lobby, conn, req.Data)
		case api.RequestTypeConfigure:
			handleConfigureRequest(timeoutCtx, lobby, conn, req.Data)
		default:
			apierrs.WebsocketErrorResponse(timeoutCtx, conn, nil, apierrs.InvalidRequestError("unknown request type"))
		}

		cancel()
	} // TODO: on start, transition to next phase with handleQuiz() with other requests handlers.
}

func handleLobbyRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn) {
	data, err := LobbyToAPIResponse(lobby)
	if err != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, err, apierrs.InternalServerError())
		return
	}
	res := &api.Response{
		Type: api.ResponseTypeLobby,
		Data: data,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		log.Println(err)
	}
}

func handleRegisterRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data any) {
	req, err := api.DecodeJSON[api.RegisterRequestData](data)
	if err != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, err, apierrs.InvalidRequestError("invalid register request"))
		return
	}

	// cancel register if user already logged in.
	if client, ok := lobby.GetPlayerByConn(conn); ok && client != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.UserAlreadyRegisteredError())
		return
	}

	if err := validateUsername(req.Username); err != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, err, apierrs.InvalidUsernameError(err.Error()))
		return
	}

	if _, _, exist := lobby.GetPlayer(req.Username); exist {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.UsernameAlreadyExistsError())
		return
	}

	client := lobby.AddPlayerWithConn(conn, req.Username)

	res := &api.Response{
		Type: api.ResponseTypeRegister,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		log.Println(err)
	}
	if err := lobby.BroadcastPlayerUpdate(ctx, client.Username(), "join"); err != nil {
		log.Println(err)
	}

	// Grant first user to join lobby owner permission.
	if lobby.Owner() == "" {
		lobby.SetOwner(req.Username)
		if err := lobby.BroadcastPlayerUpdate(ctx, req.Username, "new owner"); err != nil {
			log.Println(err)
		}
	}
}

func handleKickRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data any) {
	req, err := api.DecodeJSON[api.KickRequestData](data)
	if err != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, err, apierrs.InvalidRequestError("invalid kick request"))
		return
	}
	client, ok := lobby.GetPlayerByConn(conn)
	if !ok || client == nil {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.InvalidRequestError("user is not lobby owner"))
		return
	}
	if client.Username() != lobby.Owner() {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.InvalidRequestError("user is not lobby owner"))
		return
	}
	if ok := lobby.DeletePlayer(req.Username); !ok {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.InvalidRequestError("user not found"))
	}
	res := &api.Response{
		Type: api.ResponseTypeKick,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		log.Println(err)
	}
	if err := lobby.BroadcastPlayerUpdate(ctx, req.Username, "kick"); err != nil {
		log.Println(err)
	}
}

func handleConfigureRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data any) {
	req, err := api.DecodeJSON[api.LobbyConfigureData](data)
	if err != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, err, apierrs.InvalidRequestError("invalid configure request"))
		return
	}
	client, ok := lobby.GetPlayerByConn(conn)
	if !ok || client == nil {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.InvalidRequestError("user is not lobby owner"))
		return
	}
	if client.Username() != lobby.Owner() {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.InvalidRequestError("user is not lobby owner"))
		return
	}
	if err := lobby.SetQuiz(req.Quiz); err != nil {
		apierrs.WebsocketErrorResponse(ctx, conn, nil, apierrs.InvalidRequestError("invalid quiz selected"))
		return
	}
	res := &api.Response{
		Type: api.ResponseTypeConfigure,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		log.Println(err)
	}
	if err := lobby.BroadcastConfigure(ctx, req.Quiz); err != nil {
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
