package api

import (
	"encoding/json"
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

func DecodeJSON(data, v any) error {
	b, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
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

func DecodeErrorData(data any) (ErrorData, error) {
	res := ErrorData{}
	err := DecodeJSON(data, &res)
	return res, err
}

type RoomData struct {
	ID         string   `json:"id"`
	Owner      string   `json:"owner"`
	MaxPlayers int      `json:"maxPlayers"`
	PlayerList []string `json:"playerList"`
}

func DecodeRoomData(data any) (RoomData, error) {
	res := RoomData{}
	err := DecodeJSON(data, &res)
	return res, err
}

type CreateLobbyResponse struct {
	LobbyID string `json:"id"`
	Token   string `json:"token,omitempty"`
}

func DecodeCreateLobbyResponse(data any) (CreateLobbyResponse, error) {
	res := CreateLobbyResponse{}
	err := DecodeJSON(data, &res)
	return res, err
}

type RegisterRequestData struct {
	Username string `json:"username"`
}

type RegisterResponseData struct {
	Token string `json:"token"`
}

func DecodeRegisterResponseData(data any) (RegisterResponseData, error) {
	res := RegisterResponseData{}
	err := DecodeJSON(data, &res)
	return res, err
}

type LoginRequestData struct {
	Token string `json:"token"`
}

type LobbyUpdateResponseData struct {
	Username string `json:"username,omitempty"`
	Action   string `json:"action"`
}

func DecodeLobbyUpdateResponseData(data any) (LobbyUpdateResponseData, error) {
	res := LobbyUpdateResponseData{}
	err := DecodeJSON(data, &res)
	return res, err
}
