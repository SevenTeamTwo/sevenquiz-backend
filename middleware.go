package main

import (
	"context"
	"net/http"

	"github.com/MadAppGang/httplog"
	"github.com/google/uuid"
	"github.com/rs/cors"
)

// TODO: configure CORS per env.
var defaultCORS = cors.New(cors.Options{
	AllowedOrigins: []string{"*"},
})

var defaultHTTPLogger = httplog.LoggerWithConfig(httplog.LoggerConfig{
	RouterName: "SevenQuiz",
	// TODO: levels of log formatter.
	Formatter: httplog.ChainLogFormatter(
		httplog.DefaultLogFormatter,
		httplog.RequestHeaderLogFormatter, httplog.RequestBodyLogFormatter,
		httplog.ResponseHeaderLogFormatter, httplog.ResponseBodyLogFormatter),
	CaptureBody: true,
})

type ctxKeyRequestID int

const RequestIDKey ctxKeyRequestID = 0

func requestIDMiddleware(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		w.Header().Set("X-Request-ID", requestID)
		ctx = context.WithValue(ctx, RequestIDKey, requestID)
		h.ServeHTTP(w, r.WithContext(ctx))
	})
}

type middleware func(next http.Handler) http.Handler

func applyDefaultMiddlewares(h http.Handler) http.Handler {
	for _, mw := range []middleware{requestIDMiddleware, defaultCORS.Handler, defaultHTTPLogger} {
		h = mw(h)
	}

	return h
}
