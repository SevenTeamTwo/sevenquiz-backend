package handlers_test

import (
	"embed"
	"encoding/json"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
	"runtime"
	"sevenquiz-backend/api"
	"sevenquiz-backend/internal/client"
	"sevenquiz-backend/internal/config"
	apierrs "sevenquiz-backend/internal/errors"
	"sevenquiz-backend/internal/handlers"
	"sevenquiz-backend/internal/quiz"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var (
	//go:embed tests/quizzes
	quizzes   embed.FS
	quizzesFS fs.FS
)

func init() {
	log.SetOutput(io.Discard)

	var err error
	if quizzesFS, err = fs.Sub(quizzes, "tests/quizzes"); err != nil {
		log.Fatal(err)
	}
	defaultTestLobbyOptions.Quizzes = quizzesFS
}

var (
	defaultTestConfig = config.Config{
		JWTSecret: []byte("myjwtsecret1234"),
		Lobby: config.LobbyConf{
			MaxPlayers:      20,
			RegisterTimeout: 15 * time.Second,
		},
	}
	defaultTestUpgrader = websocket.Upgrader{
		HandshakeTimeout: 15 * time.Second,
		CheckOrigin: func(_ *http.Request) bool {
			return true // Accepting all requests
		},
	}
	defaultTestWantLobby = api.LobbyData{
		MaxPlayers:  20,
		Quizzes:     []string{"cars", "custom", "default"},
		CurrentQuiz: "cars",
	}
	defaultTestLobbyOptions = quiz.LobbyOptions{
		MaxPlayers: 20,
		Quizzes:    quizzesFS,
	}
)

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
	return client.NewClient(conn, time.Second), res, nil
}

func TestLobbyCreate(t *testing.T) {
	var (
		req     = httptest.NewRequest(http.MethodPost, "/lobby", nil)
		res     = httptest.NewRecorder()
		lobbies = &quiz.Lobbies{}
	)

	assertEqual(t, 2, runtime.NumGoroutine())
	handlers.CreateLobbyHandler(defaultTestConfig, lobbies, quizzesFS)(res, req)
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
		lobby, _ = lobbies.Register(quiz.LobbyOptions{
			MaxPlayers: 20,
			Quizzes:    quizzesFS,
		})
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, defaultTestUpgrader), "/lobby/"+lobby.ID())
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	assertLobbyBanner(t, cli, defaultTestWantLobby)
}

func TestLobbyRegister(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby, _ = lobbies.Register(defaultTestLobbyOptions)

		registerUsername = "testuser"
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, defaultTestUpgrader), "/lobby/"+lobby.ID())
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	wantLobby := defaultTestWantLobby
	registerNewOwner(t, cli, &wantLobby, registerUsername)

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

	handlers.CreateLobbyHandler(timeoutCfg, lobbies, quizzesFS)(res, req)

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
		lobby, _ = lobbies.Register(defaultTestLobbyOptions)

		ownerUsername = "owner"
		path          = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, defaultTestUpgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	wantLobby := defaultTestWantLobby
	registerNewOwner(t, cli, &wantLobby, ownerUsername)

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
		registerNewPlayer(t, cli2, &wantLobby, username)
		assertLobbyUpdate(t, cli, username, "join")

		wantLobby.PlayerList = append(wantLobby.PlayerList, username)
	}

	// Make sure new players are now in the list.
	sort.Strings(wantLobby.PlayerList)
	assertLobby(t, cli, wantLobby)

	registerUsers["testuser"].Close()
	<-time.After(time.Millisecond)
	assertLobbyUpdate(t, cli, "testuser", "disconnect")

	// Make sure disconnected player is not on the list.
	wantLobby.PlayerList = slices.Delete(wantLobby.PlayerList, 0, 1)
	assertLobby(t, cli, wantLobby)
}

func TestLobbyMaxPlayers(t *testing.T) {
	var (
		maxPlayers = 1
		lobbies    = &quiz.Lobbies{}
		lobby, _   = lobbies.Register(quiz.LobbyOptions{
			MaxPlayers: maxPlayers,
			Quizzes:    quizzesFS,
		})
		path = "/lobby/" + lobby.ID()
	)

	maxPlayersCfg := defaultTestConfig
	maxPlayersCfg.Lobby.MaxPlayers = maxPlayers

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(maxPlayersCfg, lobbies, defaultTestUpgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	wantLobby := defaultTestWantLobby
	wantLobby.MaxPlayers = maxPlayers
	registerNewOwner(t, cli, &wantLobby, "owner")

	// Make sure no players can join and be upgraded to websocket.
	_, res, err := dialTestServerWS(s, path)
	res.Body.Close()
	assertNotNil(t, err)
	assertEqual(t, http.StatusForbidden, res.StatusCode)
}

func TestLobbyOwnerElection(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby, _ = lobbies.Register(defaultTestLobbyOptions)

		ownerUsername = "owner"
		path          = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, defaultTestUpgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	wantLobby := defaultTestWantLobby
	registerNewOwner(t, cli, &wantLobby, ownerUsername)

	// Setup second player to join
	cli2, res, err := dialTestServerWS(s, path)
	res.Body.Close()
	assertNil(t, err)

	nextOwnerUsername := "nextowner"
	registerNewPlayer(t, cli2, &wantLobby, nextOwnerUsername)

	// Close owner client, must be replaced by next player.
	cli.Close()
	assertLobbyUpdate(t, cli2, ownerUsername, "disconnect")
	assertLobbyUpdate(t, cli2, nextOwnerUsername, "new owner")
	assertEqual(t, lobby.Owner(), nextOwnerUsername)

	// Close new owner client, other players so lobby must be deleted.
	cli2.Close()
	<-time.After(time.Millisecond)
	assertNil(t, lobbies.Get(lobby.ID()))
}

func TestLobbyKick(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby, _ = lobbies.Register(defaultTestLobbyOptions)

		ownerUsername = "owner"
		kickUsername  = "kick"
		path          = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, defaultTestUpgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	wantLobby := defaultTestWantLobby
	registerNewOwner(t, cli, &wantLobby, ownerUsername)

	// Setup second player to join
	cli2, res, err := dialTestServerWS(s, path)
	res.Body.Close()
	assertNil(t, err)

	registerNewPlayer(t, cli2, &wantLobby, kickUsername)
	assertLobbyUpdate(t, cli, kickUsername, "join")

	// User is not owner, kick must not be possible
	kickRes, err := cli2.Kick(ownerUsername)
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeError, kickRes.Type)

	kickRes, err = cli.Kick(kickUsername)
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeKick, kickRes.Type)
	assertLobbyUpdate(t, cli, kickUsername, "kick")
}

func TestLobbyConfigure(t *testing.T) {
	var (
		lobbies  = &quiz.Lobbies{}
		lobby, _ = lobbies.Register(defaultTestLobbyOptions)

		ownerUsername = "owner"
		path          = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, defaultTestUpgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	// Setup lobby owner
	wantLobby := defaultTestWantLobby
	registerNewOwner(t, cli, &wantLobby, ownerUsername)

	res, err := cli.Configure(wantLobby.Quizzes[1])
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeConfigure, res.Type)
	assertEqual(t, wantLobby.Quizzes[1], lobby.Quiz())
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

func registerNewPlayer(t *testing.T, cli *client.Client, wantLobby *api.LobbyData, username string) {
	t.Helper()

	assertLobbyBanner(t, cli, *wantLobby)
	assertRegister(t, cli, username)
	assertLobbyUpdate(t, cli, username, "join")
	wantLobby.PlayerList = append(wantLobby.PlayerList, username)
}

func registerNewOwner(t *testing.T, cli *client.Client, wantLobby *api.LobbyData, username string) {
	t.Helper()

	registerNewPlayer(t, cli, wantLobby, username)
	assertLobbyUpdate(t, cli, username, "new owner")
	wantLobby.Owner = &username
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
	assertEqualSlices(t, wantLobby.Quizzes, lobbyData.Quizzes)
	assertEqual(t, wantLobby.CurrentQuiz, lobbyData.CurrentQuiz)
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

func assertEqualSlices[T comparable](t *testing.T, want, got []T) {
	t.Helper()
	if !slices.Equal(want, got) {
		t.Errorf("assert equal: got %v, want %v", got, want)
	}
}
