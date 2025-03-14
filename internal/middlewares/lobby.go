package middlewares

import (
	"context"
	"log/slog"
	"net/http"
	errs "sevenquiz-backend/internal/errors"
	"sevenquiz-backend/internal/quiz"
)

type ctxKey int

const (
	LobbyKey ctxKey = iota
	LobbyIDKey
	LobbyStateKey
	LobbyUsernameKey
	LobbyRequestKey
)

func NewLobby(lobbies quiz.LobbyRepository) func(http.Handler) http.Handler {
	return func(h http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			id := r.PathValue("id")
			if id == "" {
				errs.WriteHTTPError(ctx, w, errs.MissingURLQueryError("id"))
				return
			}

			lobby, ok := lobbies.Get(id)
			if !ok || lobby == nil {
				errs.WriteHTTPError(ctx, w, errs.LobbyNotFoundError(id))
				return
			}

			pwd := r.URL.Query().Get("p")
			if !lobby.CheckPassword(pwd) {
				errs.WriteHTTPError(ctx, w, errs.UnauthorizedError("invalid lobby password"))
				return
			}

			switch lobby.State() {
			case quiz.LobbyStateRegister:
				if lobby.IsFull() {
					errs.WriteHTTPError(ctx, w, errs.TooManyPlayersError(lobby.MaxPlayers()))
					return
				}
			case quiz.LobbyStateQuiz:
				// TODO: check JWT
				// TODO: re-assign conn to player
			}

			// TODO: restitute via token and pass the LobbyPlayerKey to context

			ctx = context.WithValue(ctx, LobbyKey, lobby)
			ctx = context.WithValue(ctx, LobbyIDKey, slog.String("lobby_id", lobby.ID()))
			ctx = context.WithValue(ctx, LobbyStateKey, slog.String("lobby_state", lobby.State().String()))

			h.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
