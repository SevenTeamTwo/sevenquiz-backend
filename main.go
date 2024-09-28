package main

import (
	"embed"
	"errors"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"time"

	"sevenquiz-backend/internal/config"
	"sevenquiz-backend/internal/handlers"
	"sevenquiz-backend/internal/middlewares"
	"sevenquiz-backend/internal/quiz"

	"github.com/coder/websocket"
	"github.com/rs/cors"
	sloghttp "github.com/samber/slog-http"
)

//go:embed quizzes
var quizzes embed.FS

func init() {
	logger := slog.New(handlers.ContextHandler{
		Handler: slog.NewJSONHandler(os.Stdout, nil),
		Keys: []any{
			middlewares.LobbyIDKey,
			middlewares.LobbyStateKey,
			middlewares.LobbyUsernameKey,
			middlewares.LobbyRequestKey,
		},
	})
	slog.SetDefault(logger)
}

func main() {
	cfg, err := config.LoadConfig("") // TODO: config flags
	if err != nil {
		log.Fatal(err)
	}
	quizzesFS, err := fs.Sub(quizzes, "quizzes")
	if err != nil {
		log.Fatal(err)
	}

	var (
		lobbies    = &quiz.Lobbies{}
		acceptOpts = websocket.AcceptOptions{
			OriginPatterns: cfg.CORS.AllowedOrigins,
		}
		corsOpts = cors.Options{
			AllowedOrigins: cfg.CORS.AllowedOrigins,
		}

		defaultMws = []middlewares.Middleware{
			cors.New(corsOpts).Handler,
			sloghttp.NewWithConfig(slog.Default(), sloghttp.Config{
				WithUserAgent: true,
				WithRequestID: true,
			}),
		}
		lobbyMws = append(defaultMws, middlewares.NewLobby(lobbies))

		createLobbyHandler = handlers.CreateLobbyHandler(cfg, lobbies, quizzesFS)
		lobbyHandler       = handlers.LobbyHandler(cfg, lobbies, acceptOpts)
	)

	http.Handle("POST /lobby", middlewares.Chain(createLobbyHandler, defaultMws...))
	http.Handle("GET /lobby/{id}", middlewares.Chain(lobbyHandler, lobbyMws...))

	srv := http.Server{
		Addr:         ":8080",
		Handler:      http.DefaultServeMux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	slog.Info("starting server", slog.String("addr", srv.Addr))

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}
