package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sevenquiz-backend/api"
	errs "sevenquiz-backend/internal/errors"
	"sevenquiz-backend/internal/quiz"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func (h LobbyHandler) handleRegisterState(ctx context.Context, req api.Request[json.RawMessage], lobby *quiz.Lobby, conn *websocket.Conn) {
	switch req.Type {
	case api.RequestTypeLobby:
		handleLobbyRequest(ctx, lobby, conn, false)
	case api.RequestTypeRegister:
		handleRegisterRequest(ctx, lobby, conn, req.Data)
	case api.RequestTypeKick:
		handleKickRequest(ctx, lobby, conn, req.Data)
	case api.RequestTypeConfigure:
		handleConfigureRequest(ctx, lobby, conn, req.Data)
	case api.RequestTypeStart:
		handleStartRequest(ctx, lobby, conn, req.Data)
	default:
		err := fmt.Errorf("unknown request: %s", req.Type)
		apiErr := errs.InvalidRequestError(err, api.RequestTypeUnknown, err.Error())
		errs.WriteWebsocketError(ctx, conn, apiErr)
	}
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
	if !ok || client == nil || client.Username() != lobby.Owner() {
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
	if !ok || client == nil || client.Username() != lobby.Owner() {
		errs.WriteWebsocketError(ctx, conn, errs.UnauthorizedRequestError(api.RequestTypeConfigure, "user is not lobby owner"))
		return
	}

	if req.Quiz != "" {
		q, ok := lobby.LoadQuiz(req.Quiz)
		if !ok {
			errs.WriteWebsocketError(ctx, conn, errs.QuizNotFoundError(api.RequestTypeConfigure, "invalid quiz selected"))
			return
		}
		lobby.SetQuiz(q)
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

func handleStartRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data json.RawMessage) {
	_, err := api.DecodeJSON[api.EmptyRequestData](data)
	if err != nil {
		errs.WriteWebsocketError(ctx, conn, errs.InvalidRequestError(err, api.RequestTypeStart, "invalid start request"))
		return
	}

	client, ok := lobby.GetPlayerByConn(conn)
	if !ok || client == nil || client.Username() != lobby.Owner() {
		errs.WriteWebsocketError(ctx, conn, errs.UnauthorizedRequestError(api.RequestTypeStart, "user is not lobby owner"))
		return
	}

	q := lobby.Quiz()
	for i, question := range q.Questions {
		question.ID = i
		q.Questions[i] = question
	}

	lobby.SetQuiz(q)
	lobby.SetState(quiz.LobbyStateQuiz)
	_ = lobby.CloseUnregisteredConns()
	if err := lobby.BroadcastStart(ctx); err != nil {
		slog.Error("broadcast start", slog.Any("error", err))
	}

	go func() { //nolint:contextcheck
		for _, question := range lobby.Quiz().Questions {
			if lobby.State() == quiz.LobbyStateEnded { // All players left.
				slog.Info("quiz has ended")
				return
			}

			question.Answer = nil
			if question.Time <= 0 {
				question.Time = 30 * time.Second
			}
			lobby.SetCurrentQuestion(&question)

			start := time.Now()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			if err := lobby.BroadcastQuestion(ctx, question); err != nil {
				slog.Error("broadcast question", slog.Any("error", err))
			}
			cancel()

			deadline, cancel := context.WithDeadline(context.Background(), start.Add(question.Time))
			<-deadline.Done()
			cancel()
		}

		lobby.SetCurrentQuestion(nil)
		lobby.SetState(quiz.LobbyStateAnswers)
	}()
}
