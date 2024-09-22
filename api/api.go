package api

import "encoding/json"

const (
	ResponseTypeError       = "error"
	ResponseTypeLogin       = "login"
	ResponseTypeRegister    = "register"
	ResponseTypeRoom        = "room"
	ResponseTypeLobbyUpdate = "lobbyUpdate"
)

type Response struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

const (
	RequestTypeError    = "error"
	RequestTypeLogin    = "login"
	RequestTypeRegister = "register"
	RequestTypeRoom     = "room"
)

type Request struct {
	Type string `json:"type"`
	Data any    `json:"data,omitempty"`
}

type ErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
	Extra   any    `json:"extra,omitempty"`
}

type RoomData struct {
	ID         string   `json:"id"`
	Owner      string   `json:"owner"`
	MaxPlayers int      `json:"maxPlayers"`
	PlayerList []string `json:"playerList"`
}

type CreateLobbyResponse struct {
	LobbyID string `json:"id"`
	Token   string `json:"token,omitempty"`
}

type RegisterRequestData struct {
	Username string `json:"username"`
}

type RegisterResponseData struct {
	Token string `json:"token"`
}

type LoginRequestData struct {
	Token string `json:"token"`
}

type LobbyUpdateResponseData struct {
	Username string `json:"username,omitempty"`
	Action   string `json:"action"`
}

func DecodeJSON[T any](data any) (T, error) { //nolint: ireturn
	var res T
	b, err := json.Marshal(data)
	if err != nil {
		return res, err
	}
	if err := json.Unmarshal(b, &res); err != nil {
		return res, err
	}
	return res, nil
}
