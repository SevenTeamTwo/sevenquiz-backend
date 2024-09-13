package main

import (
	"errors"
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

var defaultHandler = func(w http.ResponseWriter, r *http.Request) {
	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Println(err)
		w.Write([]byte("could not upgrade connection to websocket"))

		return
	}
	defer conn.Close()

	conn.WriteMessage(websocket.TextMessage, []byte("Hello from server"))
}

func main() {
	http.HandleFunc("/", defaultHandler)

	srv := http.Server{
		Addr:         ":8080",
		Handler:      http.DefaultServeMux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	log.Printf("listening on addr %q\n", srv.Addr)

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
