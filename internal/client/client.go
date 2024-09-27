package client

import (
	"context"
	"net/http"
	"sevenquiz-backend/api"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

var defaultTimeout = 5 * time.Second

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

func Dial(ctx context.Context, u string, opts *websocket.DialOptions) (*Client, *http.Response, error) {
	conn, res, err := websocket.Dial(ctx, u, opts)
	if err != nil {
		return nil, nil, err
	}
	return &Client{
		conn:    conn,
		timeout: defaultTimeout,
	}, res, nil
}

func (c *Client) Close() {
	c.conn.Close(websocket.StatusNormalClosure, "client closure")
}

func (c *Client) sendCmd(req api.Request) (api.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	if err := wsjson.Write(ctx, c.conn, req); err != nil {
		return api.Response{}, err
	}
	return c.ReadResponse()
}

func (c *Client) ReadResponse() (api.Response, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()
	res := api.Response{}
	err := wsjson.Read(ctx, c.conn, &res)
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

func (c *Client) Configure(quiz string) (api.Response, error) {
	req := api.Request{
		Type: api.RequestTypeConfigure,
		Data: api.LobbyConfigureData{
			Quiz: quiz,
		},
	}
	return c.sendCmd(req)
}
