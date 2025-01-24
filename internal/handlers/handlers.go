package handlers

import (
	"context"
	"encoding/json"
	"errors"
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
			MaxPlayers:      cfg.Lobby.MaxPlayers,
			Quizzes:         quizzes, // TODO: open on system instead of embed ?
			RegisterTimeout: cfg.Lobby.RegisterTimeout,
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
	case quiz.LobbyStateRegister:
		lobby.AddConn(conn)
		// Send banner on websocket upgrade with lobby details.
		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		handleLobbyRequest(timeoutCtx, lobby, conn, true)
		cancel()
	case quiz.LobbyStateQuiz:
		// TODO: greet with current question
	}

	for {
		req, err := h.readRequest(ctx, conn)
		if err != nil {
			return
		}

		timeoutCtx, cancel := contextTimeoutWithRequest(ctx, req.Type)

		switch lobby.State() {
		case quiz.LobbyStateRegister:
			h.handleRegisterState(timeoutCtx, req, lobby, conn)
		case quiz.LobbyStateQuiz:
			h.handleQuizState(timeoutCtx, req, lobby, conn)
		case quiz.LobbyStateAnswers:
			h.handleReviewState(timeoutCtx, req, lobby, conn)
		}

		cancel()
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
		player, ok := lobby.GetPlayerByConn(conn)

		// Makes sure a player slot is freed and removed from list.
		lobby.DeletePlayerByConn(conn)

		if !ok || player == nil {
			// Conn did not register, free a player slot.
			return
		}

		timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()

		username := player.Username()

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
	case quiz.LobbyStateQuiz:
		player, ok := lobby.GetPlayerByConn(conn)
		if !ok || player == nil {
			return
		}
		player.Disconnect()

		// No other players in lobby and owner has left so discard lobby.
		if players := lobby.GetPlayerList(); len(players) == 0 {
			lobby.SetState(quiz.LobbyStateEnded)
			h.Lobbies.Delete(lobby.ID())
			return
		}
	default:
		// TODO: next stages
		// Client's connect/disconnect/login/broadcast
	}
}

func contextTimeoutWithRequest(ctx context.Context, reqType api.RequestType) (context.Context, context.CancelFunc) {
	reqCtx := context.WithValue(ctx, mws.LobbyRequestKey, slog.Any("request", reqType))
	return context.WithTimeout(reqCtx, 5*time.Second)
}

func (h LobbyHandler) readRequest(ctx context.Context, conn *websocket.Conn) (api.Request[json.RawMessage], error) {
	if h.Limiter != nil && !h.Limiter.Allow() {
		if err := h.Limiter.Wait(ctx); err != nil { // Block reading until request is permitted.
			slog.ErrorContext(ctx, "limiter wait", slog.Any("error", err))
		}
	}
	req := api.Request[json.RawMessage]{}
	err := wsjson.Read(ctx, conn, &req)
	if err != nil {
		if websocket.CloseStatus(err) == -1 { // -1 is considered as an err unrelated to closing.
			timeoutCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			errs.WriteWebsocketError(timeoutCtx, conn, errs.InvalidRequestError(err, api.RequestTypeUnknown, "could not read websocket frame"))
		} else {
			slog.ErrorContext(ctx, "ws read error", slog.Any("error", err))
		}
	}
	return req, err
}

// LobbyToAPIResponse converts a lobby to an API representation.
func LobbyToAPIResponse(lobby *quiz.Lobby) (api.LobbyResponseData, error) {
	data := api.LobbyResponseData{
		ID:          lobby.ID(),
		MaxPlayers:  lobby.MaxPlayers(),
		PlayerList:  lobby.GetPlayerList(),
		Created:     lobby.CreationDate().Format(time.RFC3339),
		Quizzes:     lobby.ListQuizzes(),
		CurrentQuiz: lobby.Quiz().Name,
	}
	if owner := lobby.Owner(); owner != "" {
		data.Owner = &owner
	}
	return data, nil
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
