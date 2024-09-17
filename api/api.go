package api

import (
	"encoding/json"
)

const (
	ResponseTypeError    = "error"
	ResponseTypeLogin    = "login"
	ResponseTypeRegister = "register"
	ResponseRoom         = "room"
	ResponseLobbyUpdate  = "lobbyUpdate"
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
	RequestRoom         = "room"
)

type Request struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type ErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
	Extra   any    `json:"extra,omitempty"`
}

type CreateLobbyResponse struct {
	LobbyID string `json:"id"`
	Token   string `json:"token"`
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
