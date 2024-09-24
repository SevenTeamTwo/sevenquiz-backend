package client

import (
	"sevenquiz-backend/api"

	"github.com/gorilla/websocket"
)

type Client struct {
	conn *websocket.Conn
}

func NewClient(conn *websocket.Conn) *Client {
	return &Client{conn: conn}
}

func (c *Client) Close() {
	c.conn.Close()
}

func (c *Client) sendCmd(req api.Request) (api.Response, error) {
	if err := c.conn.WriteJSON(req); err != nil {
		return api.Response{}, err
	}
	return c.ReadResponse()
}

func (c *Client) ReadResponse() (api.Response, error) {
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
