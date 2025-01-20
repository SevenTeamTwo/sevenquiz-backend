package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sevenquiz-backend/api"
	"sevenquiz-backend/internal/config"
	errs "sevenquiz-backend/internal/errors"
	mws "sevenquiz-backend/internal/middlewares"
	"sevenquiz-backend/internal/quiz"
	"sevenquiz-backend/internal/rate"
	"time"
	"unicode/utf8"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// CreateLobbyHandler returns a handler capable of creating new lobbies
// and storing them in the lobbies container.
func CreateLobbyHandler(cfg config.Config, lobbies quiz.LobbyRepository, quizzes map[string]api.Quiz) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		lobby, err := lobbies.Register(quiz.LobbyOptions{
			MaxPlayers: cfg.Lobby.MaxPlayers,
			Quizzes:    quizzes, // TODO: open on system instead of embed ?
			Timeout:    cfg.Lobby.RegisterTimeout,
		})
		if err != nil {
			errs.WriteHTTPError(r.Context(), w, errs.HTTPInternalServerError(err))
		}

		res := api.CreateLobbyResponseData{
			LobbyID: lobby.ID(),
		}
		if err := json.NewEncoder(w).Encode(res); err != nil {
			slog.ErrorContext(r.Context(), "lobby response encoding", slog.Any("error", err))
		}
	}
}

// LobbyToAPIResponse converts a lobby to an API representation.
func LobbyToAPIResponse(lobby *quiz.Lobby) (api.LobbyResponseData, error) {
	data := api.LobbyResponseData{
		ID:          lobby.ID(),
		MaxPlayers:  lobby.MaxPlayers(),
		PlayerList:  lobby.GetPlayerList(),
		Created:     lobby.CreationDate().Format(time.RFC3339),
		Quizzes:     lobby.ListQuizzes(),
		CurrentQuiz: lobby.Quiz(),
	}
	if owner := lobby.Owner(); owner != "" {
		data.Owner = &owner
	}
	return data, nil
}

type LobbyHandler struct {
	Config        config.Config
	Lobbies       quiz.LobbyRepository
	AcceptOptions websocket.AcceptOptions
	Limiter       *rate.Limiter
}

func (h LobbyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Lobby is passed via middleware.
	lobby, ok := ctx.Value(mws.LobbyKey).(*quiz.Lobby)
	if !ok {
		w.WriteHeader(http.StatusInternalServerError)
		slog.ErrorContext(ctx, "could not retrieve lobby")
		return
	}

	// Transition to the registration state only after a first call to the handler.
	if lobby.State() == quiz.LobbyStateCreated && lobby.NumConns() == 0 {
		lobby.SetState(quiz.LobbyStateRegister)
	}

	conn, err := websocket.Accept(w, r, &h.AcceptOptions)
	if err != nil {
		// Accept already writes a status code and error message.
		slog.ErrorContext(ctx, "ws accept", slog.Any("error", err))
		return
	}

	conn.SetReadLimit(h.Config.Lobby.WebsocketReadLimit)

	go ping(ctx, conn, 5*time.Second) // Detect timed out connection.
	defer h.handleDisconnect(ctx, lobby, conn)

	switch lobby.State() {
	case quiz.LobbyStateCreated, quiz.LobbyStateRegister:
		h.handleRegister(ctx, lobby, conn)
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
				slog.ErrorContext(ctx, "ping failed, closing conn", slog.Any("error", err))
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

func (h LobbyHandler) handleDisconnect(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn) {
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

		err := lobby.BroadcastPlayerUpdate(timeoutCtx, username, "disconnect")
		if err != nil {
			slog.ErrorContext(ctx, "broadcast player update: disconnect",
				slog.String("username", username),
				slog.Any("error", err))
		}

		if lobby.Owner() != username {
			// Conn was not owner, simply free the slot.
			return
		}

		players := lobby.GetPlayerList()

		// No other players in lobby and owner has left so discard lobby.
		if len(players) == 0 {
			h.Lobbies.Delete(lobby.ID())
			return
		}

		newOwner := players[0]
		lobby.SetOwner(newOwner)

		err = lobby.BroadcastPlayerUpdate(timeoutCtx, newOwner, "new owner")
		if err != nil {
			slog.ErrorContext(ctx, "broadcast player update: new owner",
				slog.String("username", newOwner),
				slog.Any("error", err))
		}
	default:
		// TODO: next stages
		// Client's connect/disconnect/login/broadcast
	}
}

func (h LobbyHandler) handleRegister(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn) {
	lobby.AddConn(conn)

	// Send banner on websocket upgrade with lobby details.
	timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	handleLobbyRequest(timeoutCtx, lobby, conn, true)
	cancel()

	for {
		if h.Limiter != nil && !h.Limiter.Allow() {
			if err := h.Limiter.Wait(ctx); err != nil { // Block reading until request is permitted.
				slog.ErrorContext(ctx, "limiter wait", slog.Any("error", err))
			}
		}
		req := api.Request[json.RawMessage]{}
		if err := wsjson.Read(ctx, conn, &req); err != nil {
			if websocket.CloseStatus(err) == -1 { // -1 is considered as an err unrelated to closing.
				timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				errs.WriteWebsocketError(timeoutCtx, conn, errs.InvalidRequestError(err, api.RequestTypeUnknown, "could not read websocket frame"))
			} else {
				slog.ErrorContext(ctx, "ws read error", slog.Any("error", err))
			}
			return
		}

		reqCtx := context.WithValue(ctx, mws.LobbyRequestKey, slog.Any("request", req.Type))
		timeoutCtx, cancel := context.WithTimeout(reqCtx, 5*time.Second)

		switch req.Type {
		case api.RequestTypeLobby:
			handleLobbyRequest(timeoutCtx, lobby, conn, false)
		case api.RequestTypeRegister:
			handleRegisterRequest(timeoutCtx, lobby, conn, req.Data)
		case api.RequestTypeKick:
			handleKickRequest(timeoutCtx, lobby, conn, req.Data)
		case api.RequestTypeConfigure:
			handleConfigureRequest(timeoutCtx, lobby, conn, req.Data)
		default:
			err := fmt.Errorf("unknown request: %s", req.Data)
			apiErr := errs.InvalidRequestError(err, api.RequestTypeUnknown, err.Error())
			errs.WriteWebsocketError(timeoutCtx, conn, apiErr)
		}

		cancel()
	} // TODO: on start, transition to next phase with handleQuiz() with other requests handlers.
}

func handleLobbyRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, banner bool) {
	data, err := LobbyToAPIResponse(lobby)
	if err != nil {
		apiErr := errs.InternalServerError(err, api.RequestTypeLobby)
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	res := &api.Response[api.LobbyResponseData]{
		Type: api.ResponseTypeLobby,
		Data: data,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		slog.Error("lobby response write",
			slog.Any("error", err))
		return
	}

	if !banner {
		slog.InfoContext(ctx, "successful request")
	} else {
		slog.InfoContext(ctx, "successfully sent banner")
	}
}

func handleRegisterRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data json.RawMessage) {
	req, err := api.DecodeJSON[api.RegisterRequestData](data)
	if err != nil {
		apiErr := errs.InvalidRequestError(err, api.RequestTypeRegister, "invalid register request")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	// cancel register if user already logged in.
	if client, ok := lobby.GetPlayerByConn(conn); ok && client != nil {
		apiErr := errs.UserAlreadyRegisteredError(api.RequestTypeRegister, client.Username())
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	if err := validateUsername(req.Username); err != nil {
		fields := map[string]string{"username": err.Error()}
		apiErr := errs.InputValidationError(err, api.RequestTypeRegister, fields)
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	if _, _, exist := lobby.GetPlayer(req.Username); exist {
		apiErr := errs.UsernameAlreadyExistsError(api.RequestTypeRegister, req.Username)
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	lobby.AddPlayerWithConn(conn, req.Username)

	res := &api.Response[api.EmptyResponseData]{
		Type: api.ResponseTypeRegister,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		slog.Error("register response write",
			slog.String("username", req.Username),
			slog.Any("error", err))
	}

	if err := lobby.BroadcastPlayerUpdate(ctx, req.Username, "join"); err != nil {
		slog.Error("broadcast player update: join",
			slog.String("username", req.Username),
			slog.Any("error", err))
	}

	// Grant first user to join lobby owner permission.
	if lobby.Owner() == "" {
		lobby.SetOwner(req.Username)
		if err := lobby.BroadcastPlayerUpdate(ctx, req.Username, "new owner"); err != nil {
			slog.Error("broadcast player update: new owner",
				slog.String("username", req.Username),
				slog.Any("error", err))
		}
	}

	slog.InfoContext(ctx, "successful request")
}

func handleKickRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data json.RawMessage) {
	req, err := api.DecodeJSON[api.KickRequestData](data)
	if err != nil {
		apiErr := errs.InvalidRequestError(err, api.RequestTypeKick, "invalid kick request")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	client, ok := lobby.GetPlayerByConn(conn)
	if !ok || client == nil {
		apiErr := errs.UnauthorizedRequestError(api.RequestTypeKick, "user is not lobby owner")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}
	if client.Username() != lobby.Owner() {
		apiErr := errs.UnauthorizedRequestError(api.RequestTypeKick, "user is not lobby owner")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	if ok := lobby.DeletePlayer(req.Username); !ok {
		apiErr := errs.PlayerFoundError(api.RequestTypeKick, req.Username)
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	res := &api.Response[api.EmptyResponseData]{
		Type: api.ResponseTypeKick,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		slog.Error("kick response write",
			slog.String("username", client.Username()),
			slog.String("kick", req.Username),
			slog.Any("error", err))
	}

	if err := lobby.BroadcastPlayerUpdate(ctx, req.Username, "kick"); err != nil {
		slog.Error("broadcast player update: kick",
			slog.String("username", client.Username()),
			slog.String("kick", req.Username),
			slog.Any("error", err))
	}

	slog.InfoContext(ctx, "successful request")
}

func handleConfigureRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data json.RawMessage) {
	req, err := api.DecodeJSON[api.LobbyConfigureRequestData](data)
	if err != nil {
		errs.WriteWebsocketError(ctx, conn, errs.InvalidRequestError(err, api.RequestTypeConfigure, "invalid configure request"))
		return
	}

	client, ok := lobby.GetPlayerByConn(conn)
	if !ok || client == nil {
		errs.WriteWebsocketError(ctx, conn, errs.UnauthorizedRequestError(api.RequestTypeConfigure, "user is not lobby owner"))
		return
	}
	if client.Username() != lobby.Owner() {
		errs.WriteWebsocketError(ctx, conn, errs.UnauthorizedRequestError(api.RequestTypeConfigure, "user is not lobby owner"))
		return
	}

	if req.Quiz != "" {
		if err := lobby.SetQuiz(req.Quiz); err != nil {
			errs.WriteWebsocketError(ctx, conn, errs.QuizFoundError(api.RequestTypeConfigure, "invalid quiz selected"))
			return
		}
	}
	if req.Password != "" {
		lobby.SetPassword(req.Password)
	}

	res := &api.Response[api.EmptyResponseData]{
		Type: api.ResponseTypeConfigure,
	}
	if err := wsjson.Write(ctx, conn, res); err != nil {
		slog.Error("configure response write",
			slog.String("username", client.Username()),
			slog.String("quiz", req.Quiz),
			slog.Any("error", err))
	}

	if req.Quiz != "" {
		if err := lobby.BroadcastConfigure(ctx, req.Quiz); err != nil {
			slog.Error("broadcast player update: configure",
				slog.String("username", client.Username()),
				slog.String("quiz", req.Quiz),
				slog.Any("error", err))
		}
	}

	slog.InfoContext(ctx, "successful request")
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
