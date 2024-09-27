package config

import (
	"reflect"
	"time"

	env "github.com/caarlos0/env/v11"
	"github.com/joho/godotenv"
)

type LobbyConf struct {
	MaxPlayers         int           `env:"MAX_PLAYERS"          envDefault:"25"`
	RegisterTimeout    time.Duration `env:"REGISTER_TIMEOUT"     envDefault:"15m"`
	WebsocketReadLimit int64         `env:"WEBSOCKET_READ_LIMIT" envDefault:"512"`
}

type Config struct {
	JWTSecret []byte    `env:"JWT_SECRET"`
	Lobby     LobbyConf `envPrefix:"LOBBY_"`
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		path = ".env"
	}
	if err := godotenv.Load(path); err != nil {
		return Config{}, err
	}
	cfg := Config{}
	err := env.ParseWithOptions(&cfg, env.Options{
		FuncMap: map[reflect.Type]env.ParserFunc{
			reflect.TypeOf([]byte{0}): func(v string) (interface{}, error) {
				return []byte(v), nil
			},
		},
	})
	return cfg, err
}
