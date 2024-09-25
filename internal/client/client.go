package client

import (
	"sevenquiz-backend/api"
	"time"

	"github.com/gorilla/websocket"
)

type Client struct {
	conn    *websocket.Conn
	timeout time.Duration
}

func NewClient(conn *websocket.Conn, timeout time.Duration) *Client {
	return &Client{
		conn:    conn,
		timeout: timeout,
	}
}

func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) sendCmd(req api.Request) (api.Response, error) {
	if c.timeout > 0 {
		deadline := time.Now().Add(c.timeout)
		if err := c.conn.SetWriteDeadline(deadline); err != nil {
			return api.Response{}, err
		}
	}
	if err := c.conn.WriteJSON(req); err != nil {
		return api.Response{}, err
	}
	return c.ReadResponse()
}

func (c *Client) ReadResponse() (api.Response, error) {
	if c.timeout > 0 {
		deadline := time.Now().Add(c.timeout)
		if err := c.conn.SetReadDeadline(deadline); err != nil {
			return api.Response{}, err
		}
	}
	res := api.Response{}
	err := c.conn.ReadJSON(&res)
	return res, err
}

func (c *Client) Lobby() (api.Response, error) {
	req := api.Request{
		Type: api.RequestTypeLobby,
	}
	return c.sendCmd(req)
}

func (c *Client) Register(username string) (api.Response, error) {
	req := api.Request{
		Type: api.RequestTypeRegister,
		Data: api.RegisterRequestData{
			Username: username,
		},
	}
	return c.sendCmd(req)
}

func (c *Client) Kick(username string) (api.Response, error) {
	req := api.Request{
		Type: api.RequestTypeKick,
		Data: api.KickRequestData{
			Username: username,
		},
	}
	return c.sendCmd(req)
}
