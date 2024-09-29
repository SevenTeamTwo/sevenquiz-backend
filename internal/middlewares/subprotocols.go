package middlewares

import (
	"net/http"
	"strings"
)

// Subprotocols reads the tokens smuggled inside Sec-WebSocket-Protocol
// and assign them to the according headers.
//
// This is one way to overcome the Browser clients API not being able to set
// additional headers in the websocket handshake.
//
// See https://stackoverflow.com/questions/4361173/http-headers-in-websockets-client-api
func Subprotocols(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subprotocols := r.Header.Get("Sec-WebSocket-Protocol")

		for _, protocol := range strings.Split(subprotocols, ",") {
			protocol = strings.TrimSpace(protocol)
			if strings.HasPrefix(protocol, "Bearer ") {
				r.Header.Set("Authorization", protocol[7:])
			}
		}

		h.ServeHTTP(w, r)
	})
}
