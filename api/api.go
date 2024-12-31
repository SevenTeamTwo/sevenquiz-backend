package api

import (
	"encoding/json"
)

type Response[T ResponseData] struct {
	Type    ResponseType `json:"type"`
	Message string       `json:"message,omitempty"`
	Data    T            `json:"data,omitempty"`
}

type ResponseType string

const (
	ResponseTypeError        ResponseType = "error"
	ResponseTypeRegister     ResponseType = "register"
	ResponseTypeLobby        ResponseType = "lobby"
	ResponseTypeKick         ResponseType = "kick"
	ResponseTypePlayerUpdate ResponseType = "playerUpdate"
	ResponseTypeConfigure    ResponseType = "configure"
)

func (r ResponseType) String() string {
	return string(r)
}

type Request[T RequestData] struct {
	Type RequestType `json:"type"`
	Data T           `json:"data,omitempty"`
}

type RequestType string

const (
	RequestTypeRegister  RequestType = "register"
	RequestTypeLobby     RequestType = "lobby"
	RequestTypeKick      RequestType = "kick"
	RequestTypeConfigure RequestType = "configure"
	RequestTypeUnknown   RequestType = "unknown"
)

func (r RequestType) String() string {
	return string(r)
}

type RequestData interface {
	LobbyConfigureRequestData |
		RegisterRequestData |
		KickRequestData |
		EmptyRequestData | json.RawMessage
}

type ResponseData interface {
	LobbyResponseData |
		CreateLobbyResponseData |
		PlayerUpdateResponseData |
		LobbyUpdateResponseData |
		HTTPErrorData | WebsocketErrorData |
		EmptyResponseData | json.RawMessage
}

type (
	EmptyRequestData  *struct{}
	EmptyResponseData *struct{}

	LobbyResponseData struct {
		ID          string   `json:"id"`
		Owner       *string  `json:"owner"`
		MaxPlayers  int      `json:"maxPlayers"`
		PlayerList  []string `json:"playerList"`
		Quizzes     []string `json:"quizzes"`
		CurrentQuiz string   `json:"currentQuiz"`
		Created     string   `json:"created"`
	}

	LobbyConfigureRequestData struct {
		Quiz     string `json:"quiz"`
		Password string `json:"password"`
	}

	LobbyUpdateResponseData struct {
		Quiz string `json:"quiz"`
	}

	CreateLobbyResponseData struct {
		LobbyID string `json:"id"`
	}

	RegisterRequestData struct {
		Username string `json:"username"`
	}

	KickRequestData struct {
		Username string `json:"username"`
	}

	PlayerUpdateResponseData struct {
		Username string `json:"username,omitempty"`
		Action   string `json:"action"`
	}

	AnswerData struct {
		Value []string
	}
)

func DecodeJSON[T any](data json.RawMessage) (res T, err error) {
	if err := json.Unmarshal(data, &res); err != nil {
		return res, err
	}
	return res, nil
}
