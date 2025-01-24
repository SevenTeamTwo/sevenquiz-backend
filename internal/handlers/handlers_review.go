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

func (h LobbyHandler) handleReviewState(ctx context.Context, req api.Request[json.RawMessage], lobby *quiz.Lobby, conn *websocket.Conn) {
	switch req.Type {
	case api.RequestTypeReview:
		handleReviewRequest(ctx, lobby, conn, req.Data)
	default:
		err := fmt.Errorf("unknown request: %s", req.Type)
		apiErr := errs.InvalidRequestError(err, api.RequestTypeUnknown, err.Error())
		errs.WriteWebsocketError(ctx, conn, apiErr)
	}
}

func handleReviewRequest(ctx context.Context, lobby *quiz.Lobby, conn *websocket.Conn, data json.RawMessage) {
	req, err := api.DecodeJSON[api.ReviewRequestData](data)
	if err != nil {
		apiErr := errs.InvalidRequestError(err, api.RequestTypeReview, "invalid review request")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	client, ok := lobby.GetPlayerByConn(conn)
	if !ok || client == nil || client.Username() != lobby.Owner() {
		apiErr := errs.UnauthorizedRequestError(api.RequestTypeReview, "user is not lobby owner")
		errs.WriteWebsocketError(ctx, conn, apiErr)
		return
	}

	lobby.SendReview(req.Validate)
}
