package middlewares

import (
	"net/http"
)

type Middleware func(next http.Handler) http.Handler

// Chain chains the registered middlewares in the same arguments order.
// This means the last middleware argument will be the last to be called.
func Chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}
