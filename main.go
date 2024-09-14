package main

import (
	"errors"
	"log"
	"net/http"
	"time"
)

func main() {
	// TODO: check HTTP Method.
	http.HandleFunc("/lobby", createLobbyHandler)
	http.HandleFunc("/lobby/{id}", lobbyHandler)

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
