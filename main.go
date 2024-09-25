package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"time"

	"sevenquiz-backend/internal/config"
	"sevenquiz-backend/internal/handlers"
	"sevenquiz-backend/internal/middleware"
	"sevenquiz-backend/internal/quiz"

	"github.com/MadAppGang/httplog"
	"github.com/gorilla/websocket"
	"github.com/rs/cors"
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
	cfg, err := config.LoadConfig("") // TODO: config flags
	if err != nil {
		log.Fatal(err)
	}

	lobbies := &quiz.Lobbies{}
	upgrader := websocket.Upgrader{
		HandshakeTimeout: 15 * time.Second,
		CheckOrigin: func(_ *http.Request) bool {
			return true // Accepting all requests
		},
	}

	createLobbyHandler := handlers.CreateLobbyHandler(cfg, lobbies)
	lobbyHandler := handlers.LobbyHandler(cfg, lobbies, upgrader)

	http.Handle("POST /lobby", middleware.ChainDefaults(createLobbyHandler))
	http.Handle("GET /lobby/{id}", middleware.ChainDefaults(lobbyHandler))

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
