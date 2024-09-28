package middlewares

import (
	"net/http"
)

type Middleware func(next http.Handler) http.Handler

// Chain chains the registered middlewares.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for _, mw := range mws {
		h = mw(h)
	}
	return h
}
