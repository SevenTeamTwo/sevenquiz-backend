package websocket

import (
	"sync"

	"github.com/gorilla/websocket"
)

// Conn is a wrapper around gorilla/websocket to provide thread-safe
// read/writes operations on websockets.
//
// https://pkg.go.dev/github.com/gorilla/websocket#hdr-Concurrency
type Conn struct {
	c *websocket.Conn

	// Instead of RWMutex because a websocket conn is full duplex.
	// Concurrent reads should be distinct from concurrent writes.
	// But a call to RWMutex.Lock() locks any call to RWMutex.RLock().
	rmu, wmu sync.Mutex
}

func NewConn(conn *websocket.Conn) *Conn {
	return &Conn{c: conn}
}

func (c *Conn) WriteJSON(v interface{}) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.c.WriteJSON(v)
}

func (c *Conn) ReadJSON(v interface{}) error {
	c.rmu.Lock()
	defer c.rmu.Unlock()
	return c.c.ReadJSON(v)
}

func (c *Conn) Close() error {
	return c.c.Close()
}
