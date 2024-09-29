package api

type HTTPErrorData struct {
	Code    HTTPErrorCode `json:"code"`
	Message string        `json:"message,omitempty"`
	Extra   any           `json:"extra,omitempty"`
}

type HTTPErrorCode uint8

const (
	MissingURLQueryHTTPCode     HTTPErrorCode = 101
	InternalServerErrorHTTPCode HTTPErrorCode = 102
	InvalidTokenErrorHTTPCode   HTTPErrorCode = 103
	InvalidTokenClaimHTTPCode   HTTPErrorCode = 104
	UnauthorizedErrorHTTPCode   HTTPErrorCode = 105
)

type WebsocketErrorData struct {
	Request RequestType        `json:"request,omitempty"`
	Code    WebsocketErrorCode `json:"code"`
	Message string             `json:"message,omitempty"`
	Extra   any                `json:"extra,omitempty"`
}

type WebsocketErrorCode uint8

const (
	InvalidRequestCode          WebsocketErrorCode = 201
	LobbyNotFoundCode           WebsocketErrorCode = 202
	TooManyPlayersCode          WebsocketErrorCode = 203
	PlayerAlreadyRegisteredCode WebsocketErrorCode = 204
	UsernameAlreadyExistsCode   WebsocketErrorCode = 205
	ClientRestituteCode         WebsocketErrorCode = 206
	InvalidInputCode            WebsocketErrorCode = 207
	InternalServerErrorCode     WebsocketErrorCode = 208
	UnauthorizedErrorCode       WebsocketErrorCode = 209
	PlayerNotFoundErrorCode     WebsocketErrorCode = 210
	QuizNotFoundErrorCode       WebsocketErrorCode = 211
)

type ErrorCode interface {
	HTTPErrorCode | WebsocketErrorCode
}

type ErrorData[T ErrorCode] struct { //nolint: errname
	Request RequestType `json:"request,omitempty"`
	Code    T           `json:"code"`
	Message string      `json:"message,omitempty"`
	Extra   any         `json:"extra,omitempty"`
	Err     error       `json:"error,omitempty"`
}

func (e ErrorData[T]) Error() string {
	if e.Err == nil {
		return e.Message
	}
	return e.Err.Error()
}
