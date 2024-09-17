package middleware

import (
	"context"
	"net/http"

	"github.com/MadAppGang/httplog"
	"github.com/google/uuid"
	"github.com/rs/cors"
)

type Middleware func(next http.Handler) http.Handler

// TODO: configure CORS per env.
var (
	CORS       = cors.New(cors.Options{})
	HTTPLogger = httplog.LoggerWithConfig(httplog.LoggerConfig{
		RouterName: "SevenQuiz",
		Formatter:  httplog.DefaultLogFormatter,
	})
	DefaultMiddlewares = []Middleware{RequestIDMiddleware, CORS.Handler, HTTPLogger}
)

type ctxKeyRequestID int

const RequestIDKey ctxKeyRequestID = 0

func RequestIDMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		ctx := r.Context()
		ctx = context.WithValue(ctx, RequestIDKey, requestID)

		w.Header().Set("X-Request-ID", requestID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

func ApplyDefaults(h http.Handler) http.Handler {
	for _, mw := range DefaultMiddlewares {
		h = mw(h)
	}
	return h
}
