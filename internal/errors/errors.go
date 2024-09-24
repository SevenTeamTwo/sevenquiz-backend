package errors

import (
	"encoding/json"
	"log"
	"net/http"
	"sevenquiz-backend/api"

	"sevenquiz-backend/internal/websocket"
)

const (
	InvalidRequestCode        = 101
	MissingURLQueryCode       = 102
	LobbyNotFoundCode         = 103
	TooManyPlayersCode        = 104
	UserAlreadyRegisteredCode = 105
	UsernameAlreadyExistsCode = 106
	InternalServerErrorCode   = 107
	InvalidTokenErrorCode     = 108
	InvalidTokenClaimCode     = 109
	ClientRestituteCode       = 110
	InvalidUsernameCode       = 111
)

func HTTPErrorResponse(w http.ResponseWriter, statusCode int, err error, apiErr api.ErrorData) {
	if err != nil {
		log.Println(err)
	}
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(apiErr); err != nil {
		log.Println(err)
	}
}

func WebsocketErrorResponse(conn *websocket.Conn, err error, apiErr api.ErrorData) {
	if conn == nil {
		return
	}
	if err != nil {
		log.Println(err)
	}
	res := api.Response{
		Type: api.ResponseTypeError,
		Data: apiErr,
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}
}

func InvalidRequestError(cause string) api.ErrorData {
	return api.ErrorData{
		Code:    InvalidRequestCode,
		Message: "invalid request",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}

func MissingURLQueryError(query string) api.ErrorData {
	return api.ErrorData{
		Code:    MissingURLQueryCode,
		Message: "missing url query",
		Extra: struct {
			Query string `json:"query"`
		}{
			Query: query,
		},
	}
}

func LobbyNotFoundError() api.ErrorData {
	return api.ErrorData{
		Code:    LobbyNotFoundCode,
		Message: "lobby not found",
	}
}

func TooManyPlayersError(maxPlayers int) api.ErrorData {
	return api.ErrorData{
		Code:    TooManyPlayersCode,
		Message: "too many players",
		Extra: struct {
			MaxPlayers int `json:"maxPlayers"`
		}{
			MaxPlayers: maxPlayers,
		},
	}
}

func UserAlreadyRegisteredError() api.ErrorData {
	return api.ErrorData{
		Code:    UserAlreadyRegisteredCode,
		Message: "user already registered",
	}
}

func UsernameAlreadyExistsError() api.ErrorData {
	return api.ErrorData{
		Code:    UsernameAlreadyExistsCode,
		Message: "username already exists",
	}
}

func InternalServerError() api.ErrorData {
	return api.ErrorData{
		Code:    InternalServerErrorCode,
		Message: "internal server error",
	}
}

func InvalidTokenError() api.ErrorData {
	return api.ErrorData{
		Code:    InvalidTokenErrorCode,
		Message: "invalid token",
	}
}

func InvalidTokenClaimError(claim string) api.ErrorData {
	return api.ErrorData{
		Code:    InvalidTokenClaimCode,
		Message: "invalid token claim",
		Extra: struct {
			Claim string `json:"claim"`
		}{
			Claim: claim,
		},
	}
}

func ClientRestituteError(cause string) api.ErrorData {
	return api.ErrorData{
		Code:    ClientRestituteCode,
		Message: "could not restitute client",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}

func InvalidUsernameError(cause string) api.ErrorData {
	return api.ErrorData{
		Code:    InvalidUsernameCode,
		Message: "invalid username",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}
