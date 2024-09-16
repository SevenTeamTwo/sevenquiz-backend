package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid/v3"
)

func init() {
	jwtSecret = []byte("myjwtsecret1234")
	log.SetOutput(io.Discard)
}

func newTestLobby() *lobby {
	lobby := &lobby{
		ID:            "12345",
		Created:       time.Date(2024, 01, 02, 13, 14, 15, 16, time.UTC),
		Owner:         "me",
		MaxPlayers:    25,
		tokenValidity: shortuuid.New(),
		clients:       make(map[*websocket.Conn]*client),
	}
	lobbies["12345"] = lobby

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
	lobby := newTestLobby()

	s, conn, err := setupAndDialTestServer("GET /lobby/{id}", newLobbyHandler(), "/lobby/"+lobby.ID)
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

	ok, err := compactAndCompareJSON(wantBanner, gotBanner)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !ok {
		t.Fatalf("banner mismatch, want: %s, got: %s", wantBanner, gotBanner)
	}
}

func TestLobbyRegister(t *testing.T) {
	lobby := newTestLobby()

	s, conn, err := setupAndDialTestServer("GET /lobby/{id}", newLobbyHandler(), "/lobby/"+lobby.ID)
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

	if err = writeFormattedJSON(conn, `
	{
		"type": "register",
		"data": {
			"username": %q
		}
	}`, registerUsername); err != nil {
		t.Fatalf("%v", err)
	}

	type registerCmdRes struct {
		Type string               `json:"type,omitempty"`
		Data registerResponseData `json:"data,omitempty"`
	}

	registerRes := registerCmdRes{}
	if err := conn.ReadJSON(&registerRes); err != nil {
		t.Fatalf("%v", err)
	}
	if registerRes.Type != responseTypeRegister {
		t.Fatalf("cmd response type mismatch, want: %s, got: %s", responseTypeRegister, registerRes.Type)
	}

	token, err := jwt.Parse(registerRes.Data.Token, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return jwtSecret, nil
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
	if usernameClaim != registerUsername {
		t.Fatalf("username claim mismatch, want: %s, got: %s", registerUsername, usernameClaim)
	}

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

	ok, err = compactAndCompareJSON(wantResponse, gotResponse)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !ok {
		t.Fatalf("register mismatch, want: %s, got: %s", wantResponse, gotResponse)
	}

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

	ok, err = compactAndCompareJSON(wantBroadcast, gotBroadcast)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !ok {
		t.Fatalf("broadcast mismatch, want: %s, got: %s", wantBroadcast, gotBroadcast)
	}

	if n := len(lobby.clients); n != 1 {
		t.Fatalf("clients number mismatch, want: %d, got: %d", 1, n)
	}
	for _, client := range lobby.clients {
		if client.Username != registerUsername {
			t.Fatalf("internal client mismatch, want: %s, got: %s", client.Username, registerUsername)
		}
	}
}

func TestLobbyLogin(t *testing.T) {
	lobby := newTestLobby()
	loginUsername := "testuser"

	lobby.clients[nil] = &client{Username: loginUsername}

	s, conn, err := setupAndDialTestServer("GET /lobby/{id}", newLobbyHandler(), "/lobby/"+lobby.ID)
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
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"username":      loginUsername,
		"tokenValidity": lobby.tokenValidity,
	})
	tokenStr, err := token.SignedString(jwtSecret)
	if err != nil {
		t.Fatalf("%v", err)
	}

	if err = writeFormattedJSON(conn, `
	{
		"type": "login",
		"data": {
			"token": %q
		}
	}`, tokenStr); err != nil {
		t.Fatalf("%v", err)
	}

	type loginCmdRes struct {
		Type string `json:"type,omitempty"`
	}

	loginRes := loginCmdRes{}
	if err := conn.ReadJSON(&loginRes); err != nil {
		t.Fatalf("%v", err)
	}
	if loginRes.Type != responseTypeLogin {
		t.Fatalf("cmd response type mismatch, want: %s, got: %s", responseTypeLogin, loginRes.Type)
	}

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

	ok, err := compactAndCompareJSON(wantBroadcast, gotBroadcast)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if !ok {
		t.Fatalf("broadcast mismatch, want: %s, got: %s", wantBroadcast, gotBroadcast)
	}

	if n := len(lobby.clients); n != 1 {
		t.Fatalf("clients number mismatch, want: %d, got: %d", 1, n)
	}
	for conn, client := range lobby.clients {
		if client.Username != loginUsername {
			t.Fatalf("internal client mismatch, want: %s, got: %s", client.Username, loginUsername)
		}
		if conn == nil {
			t.Fatal("client was set to nil conn")
		}
	}

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

	type cmdError struct {
		Type string       `json:"type,omitempty"`
		Data apiErrorData `json:"data,omitempty"`
	}

	errorRes := cmdError{}
	if err := conn.ReadJSON(&errorRes); err != nil {
		t.Fatalf("%v", err)
	}
	if errorRes.Type != responseTypeError {
		t.Fatalf("cmd response type mismatch, want: %s, got: %s", responseTypeError, errorRes.Type)
	}
	if errorRes.Data.Code != userAlreadyRegisteredCode {
		t.Fatalf("cmd error code mismatch, want: %d, got: %d", userAlreadyRegisteredCode, errorRes.Data.Code)
	}

	// Assert failure on tokenValidity switch.
	lobby.tokenValidity = shortuuid.New()

	if err = writeFormattedJSON(conn, `
	{
		"type": "login",
		"data": {
			"token": %q
		}
	}`, tokenStr); err != nil {
		t.Fatalf("%v", err)
	}

	errorRes = cmdError{}
	if err := conn.ReadJSON(&errorRes); err != nil {
		t.Fatalf("%v", err)
	}
	if errorRes.Type != responseTypeError {
		t.Fatalf("cmd response type mismatch, want: %s, got: %s", responseTypeError, errorRes.Type)
	}
	if errorRes.Data.Code != invalidTokenErrorCode {
		t.Fatalf("cmd error code mismatch, want: %d, got: %d", invalidTokenErrorCode, errorRes.Data.Code)
	}
}

func compactJSON(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, src); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func compactAndCompareJSON(want, got []byte) (bool, error) {
	wantBytes, err := compactJSON(want)
	if err != nil {
		return false, err
	}
	gotBytes, err := compactJSON(got)
	if err != nil {
		return false, err
	}
	return bytes.Equal(wantBytes, gotBytes), nil
}

func writeFormattedJSON(conn *websocket.Conn, format string, args ...any) error {
	msg := fmt.Sprintf(format, args...)
	return conn.WriteJSON(json.RawMessage(msg))
}
