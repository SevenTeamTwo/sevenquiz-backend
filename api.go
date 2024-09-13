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

type apiErrorResponse struct {
	Error string `json:"error"`
}

func writeJSONResponse(w http.ResponseWriter, statusCode int, v any) {
	w.WriteHeader(statusCode)

	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Println(err)
	}
}
