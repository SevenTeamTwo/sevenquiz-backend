package main

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
)

var defaultUpgrader = websocket.Upgrader{
	HandshakeTimeout: 15 * time.Second,
	CheckOrigin: func(_ *http.Request) bool {
		return true // Accepting all requests
	},
}

var jwtSecret = []byte("myjwtsecret1234")

const (
	responseTypeError    = "error"
	responseTypeLogin    = "login"
	responseTypeRegister = "register"
	responseRoom         = "room"
	responseLobbyUpdate  = "lobby_update"
)

type apiResponse struct {
	Type    string `json:"type"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data,omitempty"`
}

const (
	requestTypeError    = "error"
	requestTypeLogin    = "login"
	requestTypeRegister = "register"
	requestRoom         = "room"
)

type apiRequest struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data,omitempty"`
}

type apiErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
	Extra   any    `json:"extra,omitempty"`
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) error {
	w.WriteHeader(statusCode)
	return json.NewEncoder(w).Encode(v)
}
