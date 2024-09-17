package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"sevenquiz-api/internal/middleware"
	"sevenquiz-api/internal/quiz"

	"github.com/MadAppGang/httplog"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"
)

var (
	defaultMaxPlayers   = 25
	defaultLobbyTimeout = 15 * time.Minute
	defaultUpgrader     = websocket.Upgrader{
		HandshakeTimeout: 15 * time.Second,
		CheckOrigin: func(_ *http.Request) bool {
			return true // Accepting all requests
		},
	}
)

func init() {
	if os.Getenv("DEBUG") == "yes" {
		middleware.CORS = cors.New(cors.Options{
			AllowedOrigins: []string{"*"},
		})
		middleware.HTTPLogger = httplog.LoggerWithConfig(httplog.LoggerConfig{
			RouterName: "SevenQuiz",
			Formatter: httplog.ChainLogFormatter(
				httplog.DefaultLogFormatter,
				httplog.RequestHeaderLogFormatter, httplog.RequestBodyLogFormatter,
				httplog.ResponseHeaderLogFormatter, httplog.ResponseBodyLogFormatter),
			CaptureBody: true,
		})
	}
}

func main() {
	lobbies := &quiz.Lobbies{}

	createLobbyHandler := quiz.CreateLobbyHandler(lobbies, defaultMaxPlayers, defaultLobbyTimeout)
	http.Handle("POST /lobby", middleware.ApplyDefaults(createLobbyHandler))
	http.Handle("GET /lobby/{id}", middleware.ApplyDefaults(quiz.LobbyHandler(lobbies, defaultUpgrader)))

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
