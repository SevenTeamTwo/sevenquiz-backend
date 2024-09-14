package main

import (
	"encoding/json"
	"log"
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
)

type apiRequest struct {
	Type string         `json:"type"`
	Data map[string]any `json:"data,omitempty"`
}

type apiErrorData struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Println(err)
	}
}
