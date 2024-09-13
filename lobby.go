package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid/v3"
)

type lobby struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`

	conns map[*websocket.Conn]bool
}

var lobbies = map[string]lobby{}

type createLobbyResponse struct {
	LobbyID string `json:"lobby_id"`
	Path    string `json:"path"`
}

var createLobbyHandler = func(w http.ResponseWriter, r *http.Request) {
	var id string

	for {
		id = shortuuid.New()
		if len(id) < 5 {
			log.Println("generated id too short", id)
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		id = id[:5]

		if _, exist := lobbies[id]; !exist {
			lobbies[id] = lobby{
				ID:      id,
				Created: time.Now(),
				conns:   map[*websocket.Conn]bool{},
			}

			break
		}
	}

	res := createLobbyResponse{
		LobbyID: id,
		Path:    "/lobby/" + id,
	}

	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Println(err)
	}
}

var lobbyHandler = func(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		apiErr := apiErrorResponse{
			Error: "missing id",
		}
		writeJSONResponse(w, http.StatusBadRequest, apiErr)

		return
	}

	lobby, exist := lobbies[id]
	if !exist {
		apiErr := apiErrorResponse{
			Error: "lobby does not exist",
		}
		writeJSONResponse(w, http.StatusNotFound, apiErr)

		return
	}

	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already returns a status code and error message.
		log.Println(err)

		return
	}

	// Add conn to lobby as unregistered
	lobby.conns[conn] = false

	if err := conn.WriteJSON(lobby); err != nil {
		log.Println(err)
	}
}
