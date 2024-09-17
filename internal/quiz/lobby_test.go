package quiz_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sevenquiz-api/api"
	apierrs "sevenquiz-api/internal/errors"
	"sevenquiz-api/internal/quiz"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid/v3"
)

func init() {
	log.SetOutput(io.Discard)
}

func newTestLobby(lobbies *quiz.Lobbies) *quiz.Lobby {
	lobby := &quiz.Lobby{
		ID:            "12345",
		Created:       time.Date(2024, 01, 02, 13, 14, 15, 16, time.UTC),
		Owner:         "me",
		MaxPlayers:    25,
		TokenValidity: shortuuid.New(),
		TokenSecret:   []byte("myjwtsecret1234"),
	}
	lobbies.Register("12345", lobby)

	return lobby
}

func setupAndDialTestServer(pattern string, handler http.HandlerFunc, path string) (*httptest.Server, *websocket.Conn, error) {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, handler)

	s := httptest.NewServer(mux)
	defer s.Close()

	u := "ws" + strings.TrimPrefix(s.URL, "http")
	u += path

	conn, res, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return nil, nil, err
	}
	defer res.Body.Close()
	return s, conn, nil
}

func TestLobbyBanner(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby    = newTestLobby(lobbies)
		upgrader = websocket.Upgrader{
			HandshakeTimeout: 15 * time.Second,
			CheckOrigin: func(_ *http.Request) bool {
				return true // Accepting all requests
			},
		}
	)

	s, conn, err := setupAndDialTestServer("GET /lobby/{id}", quiz.LobbyHandler(lobbies, upgrader), "/lobby/"+lobby.ID)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer s.Close()
	defer conn.Close()

	_, gotBanner, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("%v", err)
	}

	wantBanner := []byte(`
	{
		"type": "room",
		"data":{
			"id": "12345",
			"created": "2024-01-02T13:14:15.000000016Z",
			"owner": "me",
			"maxPlayers": 25,
			"playerList": []
		}
	}
	`)

	assertEqualJSON(t, wantBanner, gotBanner)
}

func TestLobbyRegister(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby    = newTestLobby(lobbies)
		upgrader = websocket.Upgrader{
			HandshakeTimeout: 15 * time.Second,
			CheckOrigin: func(_ *http.Request) bool {
				return true // Accepting all requests
			},
		}
	)

	s, conn, err := setupAndDialTestServer("GET /lobby/{id}", quiz.LobbyHandler(lobbies, upgrader), "/lobby/"+lobby.ID)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer s.Close()
	defer conn.Close()

	// Discard banner.
	if _, _, err = conn.ReadMessage(); err != nil {
		t.Fatalf("%v", err)
	}

	registerUsername := "testuser"

	if err = writeMessagef(conn, `
	{
		"type": "register",
		"data": {
			"username": %q
		}
	}`, registerUsername); err != nil {
		t.Fatalf("%v", err)
	}

	registerRes := struct {
		Type string                   `json:"type,omitempty"`
		Data api.RegisterResponseData `json:"data,omitempty"`
	}{}

	if err := conn.ReadJSON(&registerRes); err != nil {
		t.Fatalf("%v", err)
	}
	assertEqual(t, api.ResponseTypeRegister, registerRes.Type)

	token, err := jwt.Parse(registerRes.Data.Token, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return lobby.TokenSecret, nil
	})
	if err != nil {
		t.Fatalf("%v", err)
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		t.Fatal("invalid claims")
	}
	usernameClaim, ok := claims["username"].(string)
	if !ok {
		t.Fatal("invalid username claim")
	}
	assertEqual(t, registerUsername, usernameClaim)

	// Update Token with placeholder since signature part is dynamic.
	// This is okay to modify since token was validated above.
	registerRes.Data.Token = "token_placeholder"

	gotResponse, err := json.Marshal(registerRes)
	if err != nil {
		t.Fatalf("%v", err)
	}

	wantResponse := []byte(`
	{
		"type": "register",
		"data": {
			"token":"token_placeholder"
		}
	}
	`)

	assertEqualJSON(t, wantResponse, gotResponse)

	_, gotBroadcast, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("%v", err)
	}

	wantBroadcast := []byte(fmt.Sprintf(`
	{
		"type": "lobbyUpdate",
		"data": {
			"username": %q,
			"action": "join"
		}
	}`, registerUsername))

	assertEqualJSON(t, wantBroadcast, gotBroadcast)
	assertEqual(t, 1, lobby.NumConns())

	lobby.RangeClients(func(conn *websocket.Conn, cli *quiz.Client) bool {
		assertEqual(t, registerUsername, cli.Username)
		return true
	})
}

func TestLobbyLogin(t *testing.T) {
	var (
		lobbies       = &quiz.Lobbies{}
		lobby         = newTestLobby(lobbies)
		loginUsername = "testuser"
		upgrader      = websocket.Upgrader{
			HandshakeTimeout: 15 * time.Second,
			CheckOrigin: func(_ *http.Request) bool {
				return true // Accepting all requests
			},
		}
	)

	// Setup a client to restitute.
	cli := &quiz.Client{Username: loginUsername}
	cli.Login()

	lobby.AssignConn(nil, cli)

	s, conn, err := setupAndDialTestServer("GET /lobby/{id}", quiz.LobbyHandler(lobbies, upgrader), "/lobby/"+lobby.ID)
	if err != nil {
		t.Fatalf("%v", err)
	}
	defer s.Close()
	defer conn.Close()

	// Discard banner
	if _, _, err = conn.ReadMessage(); err != nil {
		t.Fatalf("%v", err)
	}

	// Generate token with "username" claim and tokenValidity.
	token, err := lobby.NewToken(loginUsername)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if err = writeMessagef(conn, `
	{
		"type": "login",
		"data": {
			"token": %q
		}
	}`, token); err != nil {
		t.Fatalf("%v", err)
	}

	loginRes := struct {
		Type string `json:"type,omitempty"`
	}{}

	if err := conn.ReadJSON(&loginRes); err != nil {
		t.Fatalf("%v", err)
	}
	assertEqual(t, api.ResponseTypeLogin, loginRes.Type)

	_, gotBroadcast, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("%v", err)
	}

	wantBroadcast := []byte(fmt.Sprintf(`
	{
		"type": "lobbyUpdate",
		"data": {
			"username": %q,
			"action": "reconnect"
		}
	}`, loginUsername))

	assertEqualJSON(t, wantBroadcast, gotBroadcast)
	assertEqual(t, 1, lobby.NumConns())

	lobby.RangeClients(func(conn *websocket.Conn, cli *quiz.Client) bool {
		assertEqual(t, loginUsername, cli.Username)
		assertNotNil(t, conn)
		return true
	})

	// Assert error on register while already logged in.
	registerCmd := json.RawMessage(`
	{
		"type": "register",
		"data": {
			"username": "another_user"
		}
	}
	`)
	if err = conn.WriteJSON(registerCmd); err != nil {
		t.Fatalf("%v", err)
	}

	errorRes := struct {
		Type string        `json:"type,omitempty"`
		Data api.ErrorData `json:"data,omitempty"`
	}{}

	if err := conn.ReadJSON(&errorRes); err != nil {
		t.Fatalf("%v", err)
	}

	assertEqual(t, api.ResponseTypeError, errorRes.Type)
	assertEqual(t, apierrs.UserAlreadyRegisteredCode, errorRes.Data.Code)

	// Assert the token is invalidate on tokenValidity switch.
	lobby.TokenValidity = shortuuid.New()

	if err = writeMessagef(conn, `
	{
		"type": "login",
		"data": {
			"token": %q
		}
	}`, token); err != nil {
		t.Fatalf("%v", err)
	}

	errorRes = struct {
		Type string        `json:"type,omitempty"`
		Data api.ErrorData `json:"data,omitempty"`
	}{}

	if err := conn.ReadJSON(&errorRes); err != nil {
		t.Fatalf("%v", err)
	}

	assertEqual(t, api.ResponseTypeError, errorRes.Type)
	assertEqual(t, apierrs.InvalidTokenErrorCode, errorRes.Data.Code)
}

func TestLobbyTimeout(t *testing.T) {
	var (
		req = httptest.NewRequest(http.MethodPost, "/lobby?username=me", nil)
		res = httptest.NewRecorder()

		lobbies      = &quiz.Lobbies{}
		maxPlayers   = 25
		lobbyTimeout = time.Duration(0)
	)

	quiz.CreateLobbyHandler(lobbies, maxPlayers, lobbyTimeout)(res, req)

	resJSON := api.CreateLobbyResponse{}
	if err := json.NewDecoder(res.Body).Decode(&resJSON); err != nil {
		t.Fatalf("%v", err)
	}

	// wait for the goroutine to process the delete
	time.Sleep(1 * time.Millisecond)

	assertNil(t, lobbies.Get(resJSON.LobbyID))
}

func writeMessagef(conn *websocket.Conn, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return conn.WriteMessage(websocket.TextMessage, []byte(msg))
}

func compactJSON(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, src); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func assertEqualJSON(t *testing.T, want, got []byte) {
	t.Helper()

	wantBytes, err := compactJSON(want)
	if err != nil {
		t.Errorf("%v", err)
		return
	}
	gotBytes, err := compactJSON(got)
	if err != nil {
		t.Errorf("%v", err)
		return
	}

	if !bytes.Equal(wantBytes, gotBytes) {
		t.Errorf("assert equal json: got %s, want %s", wantBytes, gotBytes)
	}
}

func assertEqual(t *testing.T, want, got interface{}) {
	t.Helper()
	if want != got {
		t.Errorf("assert equal: got %v (type %v), want %v (type %v)", want, reflect.TypeOf(want), got, reflect.TypeOf(got))
	}
}

func assertNil(t *testing.T, got interface{}) {
	t.Helper()
	if !(got == nil || reflect.ValueOf(got).IsNil()) {
		t.Errorf("assert nil: got %v", got)
	}
}

func assertNotNil(t *testing.T, got interface{}) {
	t.Helper()
	if got == nil || reflect.ValueOf(got).IsNil() {
		t.Errorf("assert not nil: got %v", got)
	}
}
