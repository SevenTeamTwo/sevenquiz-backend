package api

import (
	"github.com/go-viper/mapstructure/v2"
)

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

func (r *Response) CmdData() map[string]any {
	// TODO: review.
	d, ok := r.Data.(map[string]any)
	if !ok {
		return map[string]any{}
	}
	return d
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

func DecodeErrorData(data map[string]any) (ErrorData, error) {
	res := ErrorData{}
	err := mapstructure.Decode(data, &res)
	return res, err
}

type RoomData struct {
	ID         string   `json:"id"`
	Owner      string   `json:"owner"`
	MaxPlayers int      `json:"maxPlayers"`
	PlayerList []string `json:"playerList"`
}

func DecodeRoomData(data map[string]any) (RoomData, error) {
	res := RoomData{}
	err := mapstructure.Decode(data, &res)
	return res, err
}

type CreateLobbyResponse struct {
	LobbyID string `json:"id"`
	Token   string `json:"token"`
}

func DecodeCreateLobbyResponse(data map[string]any) (CreateLobbyResponse, error) {
	res := CreateLobbyResponse{}
	err := mapstructure.Decode(data, &res)
	return res, err
}

type RegisterRequestData struct {
	Username string `json:"username"`
}

type RegisterResponseData struct {
	Token string `json:"token"`
}

func DecodeRegisterResponseData(data map[string]any) (RegisterResponseData, error) {
	res := RegisterResponseData{}
	err := mapstructure.Decode(data, &res)
	return res, err
}

type LoginRequestData struct {
	Token string `json:"token"`
}

type LobbyUpdateResponseData struct {
	Username string `json:"username,omitempty"`
	Action   string `json:"action"`
}

func DecodeLobbyUpdateResponseData(data map[string]any) (LobbyUpdateResponseData, error) {
	res := LobbyUpdateResponseData{}
	err := mapstructure.Decode(data, &res)
	return res, err
}
