package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"sevenquiz-backend/api"
	errs "sevenquiz-backend/internal/errors"
	"sevenquiz-backend/internal/quiz"

	"github.com/coder/websocket"
)

func (h LobbyHandler) handleQuizState(ctx context.Context, req api.Request[json.RawMessage], lobby *quiz.Lobby, conn *websocket.Conn) {
	switch req.Type {
	case api.RequestTypeAnswer:
		handleAnswerRequest(ctx, lobby, conn, req.Data)
	default:
		err := fmt.Errorf("unknown request: %s", req.Type)
		apiErr := errs.InvalidRequestError(err, api.RequestTypeUnknown, err.Error())
		errs.WriteWebsocketError(ctx, conn, apiErr)
	}
}

func handleAnswerRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data json.RawMessage) {
	req, err := api.DecodeJSON[api.AnswerResponseData](data)
	if err != nil {
		apiErr := errs.InvalidRequestError(err, api.RequestTypeAnswer, "invalid answer request")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}
	question := lobby.CurrentQuestion()
	if question != nil {
		player, ok := lobby.GetPlayerByConn(conn)
		if player != nil && ok {
			player.RegisterAnswer(question.ID, req.Answer)
		}
	}
}
