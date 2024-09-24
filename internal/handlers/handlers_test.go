package handlers_test

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"sevenquiz-api/api"
	"sevenquiz-api/internal/client"
	"sevenquiz-api/internal/config"
	apierrs "sevenquiz-api/internal/errors"
	"sevenquiz-api/internal/handlers"
	"sevenquiz-api/internal/quiz"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func init() {
	log.SetOutput(io.Discard)
}

var defaultTestConfig = config.Config{
	JWTSecret: []byte("myjwtsecret1234"),
	Lobby: config.LobbyConf{
		MaxPlayers:      20,
		RegisterTimeout: 15 * time.Second,
	},
}

var defaultTestWantLobby = api.LobbyData{
	MaxPlayers: 20,
}

func newTestLobby(lobbies *quiz.Lobbies) *quiz.Lobby {
	lobby, _ := lobbies.Register(quiz.LobbyOptions{
		MaxPlayers: 20,
	})
	return lobby
}

// param named "_pattern" to avoid unparam linter FP until new pattern is tested.
func setupAndDialTestServer(_pattern string, handler http.HandlerFunc, path string) (*httptest.Server, *client.Client, error) {
	s := setupTestServer(_pattern, handler)
	cli, res, err := dialTestServerWS(s, path)
	res.Body.Close()

	return s, cli, err
}

func setupTestServer(pattern string, handler http.HandlerFunc) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, handler)

	return httptest.NewServer(mux)
}

func dialTestServerWS(s *httptest.Server, path string) (*client.Client, *http.Response, error) {
	url := "ws" + strings.TrimPrefix(s.URL, "http") + path

	conn, res, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, res, err
	}

	return client.NewClient(conn), res, nil
}

func TestLobbyCreate(t *testing.T) {
	var (
		req     = httptest.NewRequest(http.MethodPost, "/lobby", nil)
		res     = httptest.NewRecorder()
		lobbies = &quiz.Lobbies{}
	)

	assertEqual(t, 2, runtime.NumGoroutine())
	handlers.CreateLobbyHandler(defaultTestConfig, lobbies)(res, req)
	assertEqual(t, 3, runtime.NumGoroutine()) // Spawns goroutine for lobby timeout

	httpRes := res.Result()
	defer httpRes.Body.Close()

	assertEqual(t, http.StatusOK, httpRes.StatusCode)

	apiRes := api.CreateLobbyResponse{}
	err := json.NewDecoder(res.Body).Decode(&apiRes)
	assertNil(t, err)

	lobby := lobbies.Get(apiRes.LobbyID)
	assertNotNil(t, lobby)
	assertEqual(t, 5, len(lobby.ID()))

	lobbies.Delete(lobby.ID())
	<-time.After(time.Millisecond)

	// Make sure timeout goroutine is cleaned up.
	assertEqual(t, 2, runtime.NumGoroutine())
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

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), "/lobby/"+lobby.ID())
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	assertLobbyBanner(t, cli, defaultTestWantLobby)
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
		registerUsername = "testuser"
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), "/lobby/"+lobby.ID())
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	assertLobbyBanner(t, cli, defaultTestWantLobby)
	assertRegister(t, cli, registerUsername)
	assertLobbyUpdate(t, cli, registerUsername, "join")
	assertLobbyUpdate(t, cli, registerUsername, "new owner")

	_, quizCli, ok := lobby.GetPlayer(registerUsername)
	assertEqual(t, true, ok)
	assertNotNil(t, quizCli)
	assertEqual(t, registerUsername, quizCli.Username())

	registerRes, err := cli.Register(registerUsername)
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeError, registerRes.Type)

	errorData, err := api.DecodeJSON[api.ErrorData](registerRes.Data)
	assertNil(t, err)
	assertEqual(t, apierrs.UserAlreadyRegisteredCode, errorData.Code)
}

func TestLobbyTimeout(t *testing.T) {
	var (
		req     = httptest.NewRequest(http.MethodPost, "/lobby", nil)
		res     = httptest.NewRecorder()
		lobbies = &quiz.Lobbies{}
	)

	timeoutCfg := defaultTestConfig
	timeoutCfg.Lobby.RegisterTimeout = time.Duration(0)

	handlers.CreateLobbyHandler(timeoutCfg, lobbies)(res, req)

	apiRes := api.CreateLobbyResponse{}
	err := json.NewDecoder(res.Body).Decode(&apiRes)
	assertNil(t, err)

	// wait for the goroutine to process the delete
	time.Sleep(time.Millisecond)

	assertNil(t, lobbies.Get(apiRes.LobbyID))
}

func TestLobbyPlayerList(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby    = newTestLobby(lobbies)
		upgrader = websocket.Upgrader{
			HandshakeTimeout: 15 * time.Second,
			CheckOrigin: func(_ *http.Request) bool {
				return true // Accepting all requests
			},
		}

		ownerUsername = "owner"
		path          = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	assertLobbyBanner(t, cli, defaultTestWantLobby)
	assertRegister(t, cli, ownerUsername)
	assertLobbyUpdate(t, cli, ownerUsername, "join")
	assertLobbyUpdate(t, cli, ownerUsername, "new owner")

	wantLobby := defaultTestWantLobby
	wantLobby.Owner = &ownerUsername
	wantLobby.PlayerList = append(wantLobby.PlayerList, ownerUsername)

	registerUsers := map[string]*client.Client{
		"testuser":  nil,
		"testuser2": nil,
		"testuser3": nil,
	}

	defer func() {
		for _, cli := range registerUsers {
			if cli == nil {
				continue
			}
			cli.Close()
		}
	}()

	for username := range registerUsers {
		cli2, res, err := dialTestServerWS(s, path)
		res.Body.Close()
		assertNil(t, err)

		registerUsers[username] = cli2
		assertLobbyBanner(t, cli2, wantLobby)
		assertRegister(t, cli2, username)
		assertLobbyUpdate(t, cli, username, "join")
		assertLobbyUpdate(t, cli2, username, "join")

		wantLobby.PlayerList = append(wantLobby.PlayerList, username)
	}

	sort.Strings(wantLobby.PlayerList)
	assertLobby(t, cli, wantLobby)

	registerUsers["testuser"].Close()
	<-time.After(time.Millisecond)
	assertLobbyUpdate(t, cli, "testuser", "disconnect")

	wantLobby.PlayerList = slices.Delete(wantLobby.PlayerList, 0, 1)
	assertLobby(t, cli, wantLobby)
}

func TestLobbyMaxPlayers(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		upgrader = websocket.Upgrader{
			HandshakeTimeout: 15 * time.Second,
			CheckOrigin: func(_ *http.Request) bool {
				return true // Accepting all requests
			},
		}
		maxPlayers = 1
	)

	lobby, err := lobbies.Register(quiz.LobbyOptions{
		MaxPlayers: maxPlayers,
	})
	assertNil(t, err)

	path := "/lobby/" + lobby.ID()
	maxPlayersCfg := defaultTestConfig
	maxPlayersCfg.Lobby.MaxPlayers = maxPlayers

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(maxPlayersCfg, lobbies, upgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	ownerUsername := "owner"
	wantLobby := defaultTestWantLobby
	wantLobby.MaxPlayers = maxPlayers
	assertLobbyBanner(t, cli, wantLobby)
	assertRegister(t, cli, ownerUsername)
	assertLobbyUpdate(t, cli, ownerUsername, "join")

	_, res, err := dialTestServerWS(s, path)
	res.Body.Close()
	assertNotNil(t, err)
	assertEqual(t, http.StatusForbidden, res.StatusCode)
}

func TestLobbyOwnerElection(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby    = newTestLobby(lobbies)
		upgrader = websocket.Upgrader{
			HandshakeTimeout: 15 * time.Second,
			CheckOrigin: func(_ *http.Request) bool {
				return true // Accepting all requests
			},
		}

		ownerUsername = "owner"
		path          = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	wantLobby := defaultTestWantLobby

	// Setup lobby owner
	assertLobbyBanner(t, cli, defaultTestWantLobby)
	assertRegister(t, cli, ownerUsername)
	assertLobbyUpdate(t, cli, ownerUsername, "join")
	wantLobby.Owner = &ownerUsername
	wantLobby.PlayerList = append(wantLobby.PlayerList, ownerUsername)

	// Setup second player to join
	cli2, res, err := dialTestServerWS(s, path)
	res.Body.Close()
	assertNil(t, err)

	nextOwnerUsername := "nextowner"
	assertLobbyBanner(t, cli2, wantLobby)
	assertRegister(t, cli2, nextOwnerUsername)
	assertLobbyUpdate(t, cli2, nextOwnerUsername, "join")

	cli.Close()
	assertLobbyUpdate(t, cli2, ownerUsername, "disconnect")
	assertLobbyUpdate(t, cli2, nextOwnerUsername, "new owner")
	assertEqual(t, lobby.Owner(), nextOwnerUsername)

	cli2.Close()
	<-time.After(time.Millisecond)
	assertNil(t, lobbies.Get(lobby.ID()))
}

func assertLobby(t *testing.T, cli *client.Client, wantLobby api.LobbyData) {
	t.Helper()

	res, err := cli.Lobby()
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeLobby, res.Type)

	lobbyData, err := api.DecodeJSON[api.LobbyData](res.Data)
	assertNil(t, err)

	if wantLobby.Owner == nil {
		assertNil(t, lobbyData.Owner)
	} else {
		assertNotNil(t, lobbyData.Owner)
		assertEqual(t, *wantLobby.Owner, *lobbyData.Owner)
	}
	assertEqual(t, wantLobby.MaxPlayers, lobbyData.MaxPlayers)
	assertEqual(t, true, len(lobbyData.ID) == 5)
	assertEqual(t, true, len(lobbyData.Created) > 0)
}

func assertLobbyBanner(t *testing.T, cli *client.Client, wantLobby api.LobbyData) {
	t.Helper()

	lobbyRes, err := cli.ReadResponse()
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeLobby, lobbyRes.Type)

	lobbyData, err := api.DecodeJSON[api.LobbyData](lobbyRes.Data)
	assertNil(t, err)

	if wantLobby.Owner == nil {
		assertNil(t, lobbyData.Owner)
	} else {
		assertNotNil(t, lobbyData.Owner)
		assertEqual(t, *wantLobby.Owner, *lobbyData.Owner)
	}
	assertEqual(t, wantLobby.MaxPlayers, lobbyData.MaxPlayers)
	assertEqual(t, true, len(lobbyData.ID) == 5)
	assertEqual(t, true, len(lobbyData.Created) > 0)
}

func assertRegister(t *testing.T, cli *client.Client, username string) {
	t.Helper()

	res, err := cli.Register(username)
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeRegister, res.Type)
}

func assertLobbyUpdate(t *testing.T, cli *client.Client, username, action string) {
	t.Helper()

	res, err := cli.ReadResponse()
	assertNil(t, err)

	lobbyUpdateData, err := api.DecodeJSON[api.PlayerUpdateResponseData](res.Data)
	assertNil(t, err)

	assertEqual(t, res.Type, api.ResponseTypePlayerUpdate)
	assertEqual(t, username, lobbyUpdateData.Username)
	assertEqual(t, action, lobbyUpdateData.Action)
}

func assertEqual(t *testing.T, want, got interface{}) {
	t.Helper()
	if want != got {
		t.Errorf("assert equal: got %v (type %v), want %v (type %v)", got, reflect.TypeOf(got), want, reflect.TypeOf(want))
	}
}

func assertNil(t *testing.T, got interface{}) {
	t.Helper()
	if !(got == nil || reflect.ValueOf(got).IsNil()) {
		t.Fatalf("assert nil: got %v", got)
	}
}

func assertNotNil(t *testing.T, got interface{}) {
	t.Helper()
	if got == nil || reflect.ValueOf(got).IsNil() {
		t.Fatalf("assert not nil: got %v", got)
	}
}
