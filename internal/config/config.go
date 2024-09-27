package config

import (
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

var (
	defaultMaxPlayers         = 25
	defaultLobbyTimeout       = 15 * time.Minute
	defaultWebsocketReadLimit = int64(512)
)

type LobbyConf struct {
	MaxPlayers         int
	RegisterTimeout    time.Duration
	WebsocketReadLimit int64
}

type Config struct {
	JWTSecret []byte
	Lobby     LobbyConf
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		path = ".env"
	}
	if err := godotenv.Load(path); err != nil {
		return Config{}, err
	}

	cfg := Config{
		JWTSecret: []byte(os.Getenv("JWT_SECRET")),
		Lobby: LobbyConf{
			MaxPlayers:         defaultMaxPlayers,
			RegisterTimeout:    defaultLobbyTimeout,
			WebsocketReadLimit: defaultWebsocketReadLimit,
		},
	}

	var err error
	if maxPlayers := os.Getenv("LOBBY_MAX_PLAYERS"); maxPlayers != "" {
		cfg.Lobby.MaxPlayers, err = strconv.Atoi(maxPlayers)
		if err != nil {
			return cfg, err
		}
	}
	if lobbyTimeout := os.Getenv("LOBBY_REGISTER_TIMEOUT"); lobbyTimeout != "" {
		cfg.Lobby.RegisterTimeout, err = time.ParseDuration(lobbyTimeout)
		if err != nil {
			return cfg, err
		}
	}
	if readLimit := os.Getenv("LOBBY_WEBSOCKET_READ_LIMIT"); readLimit != "" {
		cfg.Lobby.WebsocketReadLimit, err = strconv.ParseInt(readLimit, 10, 64)
		if err != nil {
			return cfg, err
		}
	}

	return cfg, nil
}
