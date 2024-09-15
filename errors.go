package main

import (
	"log"
	"net/http"

	"github.com/gorilla/websocket"
)

func httpErrorResponse(w http.ResponseWriter, statusCode int, err error, apiErr apiErrorData) {
	if err != nil {
		log.Println(err)
	}
	if err := writeJSON(w, statusCode, apiErr); err != nil {
		log.Println(err)
	}
}

func websocketErrorResponse(conn *websocket.Conn, err error, apiErr apiErrorData) {
	if conn == nil {
		return
	}
	if err != nil {
		log.Println(err)
	}
	res := apiResponse{
		Type: responseTypeError,
		Data: apiErr,
	}
	if err := conn.WriteJSON(res); err != nil {
		log.Println(err)
	}
}

const (
	invalidRequestCode        = 101
	missingURLQueryCode       = 102
	lobbyNotFoundCode         = 103
	tooManyPlayersCode        = 104
	userAlreadyRegisteredCode = 105
	usernameAlreadyExistsCode = 106
	internalServerErrorCode   = 107
	invalidTokenErrorCode     = 108
	invalidTokenClaimCode     = 109
	clientRestituteCode       = 110
	invalidUsernameCode       = 111
)

func newInvalidRequestError(cause string) apiErrorData {
	return apiErrorData{
		Code:    invalidRequestCode,
		Message: "invalid request",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}

func newMissingURLQueryError(query string) apiErrorData {
	return apiErrorData{
		Code:    missingURLQueryCode,
		Message: "missing url query",
		Extra: struct {
			Query string `json:"query"`
		}{
			Query: query,
		},
	}
}

func newLobbyNotFoundError() apiErrorData {
	return apiErrorData{
		Code:    lobbyNotFoundCode,
		Message: "lobby not found",
	}
}

func newTooManyPlayersError(maxPlayers uint64) apiErrorData {
	return apiErrorData{
		Code:    tooManyPlayersCode,
		Message: "too many players",
		Extra: struct {
			MaxPlayers uint64 `json:"max_players"`
		}{
			MaxPlayers: maxPlayers,
		},
	}
}

func newUserAlreadyRegisteredError() apiErrorData {
	return apiErrorData{
		Code:    userAlreadyRegisteredCode,
		Message: "user already registered",
	}
}

func newUsernameAlreadyExistsError() apiErrorData {
	return apiErrorData{
		Code:    usernameAlreadyExistsCode,
		Message: "username already exists",
	}
}

func newInternalServerError() apiErrorData {
	return apiErrorData{
		Code:    internalServerErrorCode,
		Message: "internal server error",
	}
}

func newInvalidTokenError() apiErrorData {
	return apiErrorData{
		Code:    invalidTokenErrorCode,
		Message: "invalid token",
	}
}

func newInvalidTokenClaimError(claim string) apiErrorData {
	return apiErrorData{
		Code:    invalidTokenClaimCode,
		Message: "invalid token claim",
		Extra: struct {
			Claim string `json:"claim"`
		}{
			Claim: claim,
		},
	}
}

func newClientRestituteError(cause string) apiErrorData {
	return apiErrorData{
		Code:    clientRestituteCode,
		Message: "could not restitute client",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}

func newInvalidUsernameError(cause string) apiErrorData {
	return apiErrorData{
		Code:    invalidUsernameCode,
		Message: "invalid username",
		Extra: struct {
			Cause string `json:"cause"`
		}{
			Cause: cause,
		},
	}
}
