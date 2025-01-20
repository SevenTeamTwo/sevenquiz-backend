package main

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"log"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"sevenquiz-backend/api"
	"sevenquiz-backend/internal/config"
	"sevenquiz-backend/internal/handlers"
	mws "sevenquiz-backend/internal/middlewares"
	"sevenquiz-backend/internal/quiz"
	"sevenquiz-backend/internal/rate"

	"github.com/coder/websocket"
	"github.com/rs/cors"
	sloghttp "github.com/samber/slog-http"
	"gopkg.in/yaml.v3"
)

//go:embed quizzes
var quizzes embed.FS

func init() {
	logger := slog.New(handlers.ContextHandler{
		Handler: slog.NewJSONHandler(os.Stdout, nil),
		Keys: []any{
			mws.LobbyIDKey,
			mws.LobbyStateKey,
			mws.LobbyUsernameKey,
			mws.LobbyRequestKey,
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

	quizzes := map[string]api.Quiz{}

	root := "."
	depth := 0

	err = fs.WalkDir(quizzesFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() && strings.Count(path, "/") <= depth {
			path := d.Name() + "/questions.yml"
			f, err := quizzesFS.Open(path)
			if err != nil {
				return err
			}
			quiz := api.Quiz{Name: d.Name()}
			dec := yaml.NewDecoder(f)
			for {
				var q api.Question
				if err := dec.Decode(&q); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					quiz.Questions = []api.Question{}
					return err
				}
				quiz.Questions = append(quiz.Questions, q)
			}
			quizzes[quiz.Name] = quiz
		}
		return nil
	})
	if err != nil {
		log.Fatal(err)
	}

	var (
		lobbies    = quiz.NewLobbiesCache()
		acceptOpts = websocket.AcceptOptions{
			OriginPatterns: cfg.CORS.AllowedOrigins,
		}
		corsOpts = cors.Options{
			AllowedOrigins: cfg.CORS.AllowedOrigins,
		}

		defaultMws = []mws.Middleware{
			cors.New(corsOpts).Handler,
			sloghttp.NewWithConfig(slog.Default(), sloghttp.Config{
				WithUserAgent: true,
				WithRequestID: true,
			}),
		}
		lobbyMws = append(defaultMws, mws.Subprotocols, mws.NewLobby(lobbies))

		createLobbyHandler = handlers.CreateLobbyHandler(cfg, lobbies, quizzes)
		lobbyHandler       = handlers.LobbyHandler{
			Config:        cfg,
			Lobbies:       lobbies,
			AcceptOptions: acceptOpts,
		}
	)

	if cfg.RequestsRateLimit > 0 {
		lobbyHandler.Limiter = rate.NewLimiter(time.Second, cfg.RequestsRateLimit)
	}

	http.Handle("POST /lobby", mws.Chain(createLobbyHandler, defaultMws...))
	http.Handle("GET /lobby/{id}", mws.Chain(lobbyHandler, lobbyMws...))

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
