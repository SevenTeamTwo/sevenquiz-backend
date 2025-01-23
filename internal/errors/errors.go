package errors

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sevenquiz-backend/api"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var errorCodeHTTPStatusCode = map[api.HTTPErrorCode]int{
	api.MissingURLQueryHTTPCode:     http.StatusBadRequest,
	api.InternalServerErrorHTTPCode: http.StatusInternalServerError,
	api.InvalidTokenErrorHTTPCode:   http.StatusForbidden,
	api.InvalidTokenClaimHTTPCode:   http.StatusForbidden,
	api.UnauthorizedErrorHTTPCode:   http.StatusUnauthorized,
}

func WriteHTTPError(ctx context.Context, w http.ResponseWriter, err error) {
	res := api.HTTPErrorData{}

	if err == nil {
		statusCode := http.StatusInternalServerError
		slog.ErrorContext(ctx, "http error", slog.Int("status_code", statusCode))
		w.WriteHeader(statusCode)

		res.Code = api.InternalServerErrorHTTPCode
		res.Message = "unexpected error"
		if err := json.NewEncoder(w).Encode(res); err != nil {
			slog.ErrorContext(ctx, "http error: failed to encode response", slog.Any("error", err))
		}
		return
	}

	statusCode := http.StatusInternalServerError

	apiErr := &api.ErrorData[api.HTTPErrorCode]{}
	if errors.As(err, apiErr) {
		res.Code = apiErr.Code
		res.Message = apiErr.Message
		res.Extra = apiErr.Extra
		if code, ok := errorCodeHTTPStatusCode[apiErr.Code]; ok {
			statusCode = code
		}
	} else {
		res.Code = api.InternalServerErrorHTTPCode
		res.Message = "unexpected error"
	}

	slog.ErrorContext(ctx, "http error",
		slog.Any("error", err),
		slog.Any("error_code", res.Code),
		slog.Int("status_code", statusCode))

	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(res); err != nil {
		slog.ErrorContext(ctx, "http error: failed to encode response", slog.Any("error", err))
	}
}

func WriteWebsocketError(ctx context.Context, conn *websocket.Conn, err error) {
	res := api.Response[api.WebsocketErrorData]{
		Type: api.ResponseTypeError,
	}

	if err == nil {
		slog.ErrorContext(ctx, "ws nil error")

		res.Data.Code = api.InternalServerErrorCode
		res.Message = "unexpected error"
		if err := wsjson.Write(ctx, conn, res); err != nil {
			slog.ErrorContext(ctx, "ws error: failed to write response", slog.Any("error", err))
		}
		return
	}

	apiErr := &api.ErrorData[api.WebsocketErrorCode]{}
	if errors.As(err, apiErr) {
		res.Data.Code = apiErr.Code
		res.Data.Message = apiErr.Message
		res.Data.Extra = apiErr.Extra
	} else {
		res.Data.Code = api.InternalServerErrorCode
		res.Data.Message = "unexpected error"
	}

	slog.ErrorContext(ctx, "ws error",
		slog.Any("error", err),
		slog.Any("error_code", res.Data.Code))

	if err := wsjson.Write(ctx, conn, res); err != nil {
		slog.ErrorContext(ctx, "ws error: failed to write response", slog.Any("error", err))
	}
}

func InvalidRequestError(err error, req api.RequestType, cause string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.InvalidRequestCode,
		Message: "invalid request",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
		Err: err,
	}
}

func UnauthorizedRequestError(req api.RequestType, cause string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.UnauthorizedErrorCode,
		Message: "unauthorized request",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}

func MissingURLQueryError(query string) api.ErrorData[api.HTTPErrorCode] {
	return api.ErrorData[api.HTTPErrorCode]{
		Code:    api.MissingURLQueryHTTPCode,
		Message: "missing url query",
		Extra: struct {
			Query string `json:"query"`
		}{
			Query: query,
		},
	}
}

func UnauthorizedError(cause string) api.ErrorData[api.HTTPErrorCode] {
	return api.ErrorData[api.HTTPErrorCode]{
		Code:    api.UnauthorizedErrorHTTPCode,
		Message: "unauthorized",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}

func LobbyNotFoundError(lobbyID string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Code:    api.LobbyNotFoundCode,
		Message: "lobby not found",
		Extra: struct {
			LobbyID string `json:"lobbyID"`
		}{
			LobbyID: lobbyID,
		},
	}
}

func PlayerFoundError(req api.RequestType, username string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.PlayerNotFoundErrorCode,
		Message: "player not found",
		Extra: struct {
			Username string `json:"username"`
		}{
			Username: username,
		},
	}
}

func QuizNotFoundError(req api.RequestType, quiz string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.QuizNotFoundErrorCode,
		Message: "quiz not found",
		Extra: struct {
			Quiz string `json:"quiz"`
		}{
			Quiz: quiz,
		},
	}
}

func TooManyPlayersError(maxPlayers int) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Code:    api.TooManyPlayersCode,
		Message: "too many players",
		Extra: struct {
			MaxPlayers int `json:"maxPlayers"`
		}{
			MaxPlayers: maxPlayers,
		},
	}
}

func UserAlreadyRegisteredError(req api.RequestType, username string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.PlayerAlreadyRegisteredCode,
		Message: "user already registered",
		Extra: struct {
			Username string `json:"username"`
		}{
			Username: username,
		},
	}
}

func UsernameAlreadyExistsError(req api.RequestType, username string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.UsernameAlreadyExistsCode,
		Message: "username already exists",
		Extra: struct {
			Username string `json:"username"`
		}{
			Username: username,
		},
	}
}

func HTTPInternalServerError(err error) api.ErrorData[api.HTTPErrorCode] {
	return api.ErrorData[api.HTTPErrorCode]{
		Code:    api.InternalServerErrorHTTPCode,
		Message: "internal server error",
		Err:     err,
	}
}

func InternalServerError(err error, req api.RequestType) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.InternalServerErrorCode,
		Message: "internal server error",
		Err:     err,
	}
}

func InvalidTokenError(err error, req api.RequestType) api.ErrorData[api.HTTPErrorCode] {
	return api.ErrorData[api.HTTPErrorCode]{
		Request: req,
		Code:    api.InvalidTokenErrorHTTPCode,
		Message: "invalid token",
		Err:     err,
	}
}

func InvalidTokenClaimError(err error, req api.RequestType, claim string) api.ErrorData[api.HTTPErrorCode] {
	return api.ErrorData[api.HTTPErrorCode]{
		Request: req,
		Code:    api.InvalidTokenClaimHTTPCode,
		Message: "invalid token claim",
		Extra: struct {
			Claim string `json:"claim"`
		}{
			Claim: claim,
		},
		Err: err,
	}
}

func ClientRestituteError(err error, req api.RequestType, cause string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.ClientRestituteCode,
		Message: "could not restitute client",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
		Err: err,
	}
}

func InputValidationError(err error, req api.RequestType, fields map[string]string) api.ErrorData[api.WebsocketErrorCode] {
	return api.ErrorData[api.WebsocketErrorCode]{
		Request: req,
		Code:    api.InvalidInputCode,
		Message: "invalid input",
		Extra:   fields,
		Err:     err,
	}
}
