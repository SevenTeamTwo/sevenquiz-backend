package config

import (
	"os"
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

type CORSConf struct {
	AllowedOrigins []string `env:"ALLOWED_ORIGINS" envDefault:"*"`
}

type Config struct {
	JWTSecret         []byte    `env:"JWT_SECRET"`
	CORS              CORSConf  `envPrefix:"CORS_"`
	Lobby             LobbyConf `envPrefix:"LOBBY_"`
	RequestsRateLimit int       `env:"REQUESTS_RATE_LIMIT" envDefault:"30"`
}

func LoadConfig(path string) (Config, error) {
	if path == "" {
		path = ".env"
	}
	if _, err := os.Stat(path); err == nil {
		if err = godotenv.Load(path); err != nil {
			return Config{}, err
		}
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
