package handlers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"reflect"
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
	"github.com/lithammer/shortuuid/v3"
)

func init() {
	log.SetOutput(io.Discard)
}

var defaultTestConfig = config.Config{
	JWTSecret: []byte("myjwtsecret1234"),
	Lobby: config.LobbyConf{
		MaxPlayers:      25,
		RegisterTimeout: 15 * time.Second,
	},
}

var defaultTestWantRoom = api.RoomData{
	Owner:      "owner",
	MaxPlayers: 20,
	PlayerList: []string{},
}

func newTestLobby(lobbies *quiz.Lobbies) *quiz.Lobby {
	lobby, _ := lobbies.Register(quiz.LobbyOptions{
		Owner:      "owner",
		MaxPlayers: 20,
	})
	return lobby
}

// param named "_pattern" to avoid unparam linter FP until new pattern is tested.
func setupAndDialTestServer(_pattern string, handler http.HandlerFunc, path string) (*httptest.Server, *client.Client, error) {
	s := setupTestServer(_pattern, handler)
	cli, err := dialTestServerWS(s, path)

	return s, cli, err
}

func setupTestServer(pattern string, handler http.HandlerFunc) *httptest.Server {
	mux := http.NewServeMux()
	mux.HandleFunc(pattern, handler)

	return httptest.NewServer(mux)
}

func dialTestServerWS(s *httptest.Server, path string) (*client.Client, error) {
	url := "ws" + strings.TrimPrefix(s.URL, "http") + path

	conn, res, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()

	return client.NewClient(conn), nil
}

func TestLobbyCreate(t *testing.T) {
	var (
		req     = httptest.NewRequest(http.MethodPost, "/lobby?username=owner", nil)
		res     = httptest.NewRecorder()
		lobbies = &quiz.Lobbies{}
	)

	handlers.CreateLobbyHandler(defaultTestConfig, lobbies)(res, req)

	httpRes := res.Result()
	defer httpRes.Body.Close()

	assertEqual(t, http.StatusOK, httpRes.StatusCode)

	apiRes := api.CreateLobbyResponse{}
	err := json.NewDecoder(res.Body).Decode(&apiRes)
	assertNil(t, err)

	lobby := lobbies.Get(apiRes.LobbyID)
	assertNotNil(t, lobby)

	_, err = lobby.CheckToken(defaultTestConfig, apiRes.Token)
	assertNil(t, err)
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

	assertRoomBanner(t, cli, defaultTestWantRoom)
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

	assertEqual(t, 1, lobby.NumConns())
	assertRoomBanner(t, cli, defaultTestWantRoom)
	assertRegister(t, cli, lobby, registerUsername)

	assertLobbyUpdate(t, cli, registerUsername, "join")

	assertEqual(t, 1, lobby.NumConns())

	_, quizCli, ok := lobby.GetClient(registerUsername)
	assertEqual(t, true, ok)
	assertNotNil(t, quizCli)

	assertEqual(t, registerUsername, quizCli.Username())
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
	lobby.NewClient(loginUsername, nil)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), "/lobby/"+lobby.ID())
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	assertEqual(t, 1, lobby.NumConns())
	assertRoomBanner(t, cli, defaultTestWantRoom)

	token, err := lobby.NewToken(defaultTestConfig, loginUsername)
	assertNil(t, err)

	assertLogin(t, cli, token)
	assertLobbyUpdate(t, cli, loginUsername, "reconnect")
	assertEqual(t, 1, lobby.NumConns())

	// Assert error on register while already logged in.
	registerRes, err := cli.Register(loginUsername)
	assertNil(t, err)

	errorData, err := api.DecodeJSON[api.ErrorData](registerRes.Data)
	assertNil(t, err)

	assertEqual(t, registerRes.Type, api.ResponseTypeError)
	assertEqual(t, apierrs.UserAlreadyRegisteredCode, errorData.Code)

	// Assert the token is invalidate on tokenValidity switch.
	lobby.SetTokenValidity(shortuuid.New())

	loginRes, err := cli.Login(token)
	assertNil(t, err)

	errorData, err = api.DecodeJSON[api.ErrorData](loginRes.Data)
	assertNil(t, err)
	assertEqual(t, loginRes.Type, api.ResponseTypeError)
	assertEqual(t, apierrs.InvalidTokenErrorCode, errorData.Code)
}

func TestLobbyLoginOwner(t *testing.T) {
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

	// Setup the owner to restitute
	lobby.NewClient(lobby.Owner(), nil)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), "/lobby/"+lobby.ID())
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	assertRoomBanner(t, cli, defaultTestWantRoom)

	token, err := lobby.NewToken(defaultTestConfig, lobby.Owner())
	assertNil(t, err)

	assertLogin(t, cli, token)
	assertLobbyUpdate(t, cli, lobby.Owner(), "join")
	assertEqual(t, 1, lobby.NumConns())
}

func TestLobbyTimeout(t *testing.T) {
	var (
		req     = httptest.NewRequest(http.MethodPost, "/lobby?username=me", nil)
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
	time.Sleep(1 * time.Millisecond)

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

		path = "/lobby/" + lobby.ID()
	)

	s, cli, err := setupAndDialTestServer("GET /lobby/{id}", handlers.LobbyHandler(defaultTestConfig, lobbies, upgrader), path)
	assertNil(t, err)
	defer s.Close()
	defer cli.Close()

	assertRoomBanner(t, cli, defaultTestWantRoom)

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

	wantLobby := defaultTestWantRoom

	for username := range registerUsers {
		cli2, err := dialTestServerWS(s, path)
		assertNil(t, err)

		registerUsers[username] = cli2
		assertRoomBanner(t, cli2, wantLobby)
		assertRegister(t, cli2, lobby, username)
		assertLobbyUpdate(t, cli, username, "join")
		assertLobbyUpdate(t, cli2, username, "join")

		wantLobby.PlayerList = append(wantLobby.PlayerList, username)
	}

	sort.Strings(wantLobby.PlayerList)

	assertRoom(t, cli, wantLobby)

	registerUsers["testuser"].Close()

	// Give time to acknowledge closure.
	<-time.After(10 * time.Millisecond)

	assertLobbyUpdate(t, cli, "testuser", "disconnect")

	wantLobby.PlayerList = slices.Delete(wantLobby.PlayerList, 0, 1)

	assertRoom(t, cli, wantLobby)
}

func assertRoom(t *testing.T, cli *client.Client, wantRoom api.RoomData) {
	t.Helper()

	res, err := cli.Room()
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeRoom, res.Type)

	roomData, err := api.DecodeJSON[api.RoomData](res.Data)
	assertNil(t, err)

	assertEqual(t, wantRoom.Owner, roomData.Owner)
	assertEqual(t, wantRoom.MaxPlayers, roomData.MaxPlayers)
	assertEqual(t, true, len(roomData.ID) == 5)
}

func assertRoomBanner(t *testing.T, cli *client.Client, wantRoom api.RoomData) {
	t.Helper()

	roomRes, err := cli.ReadResponse()
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeRoom, roomRes.Type)

	roomData, err := api.DecodeJSON[api.RoomData](roomRes.Data)
	assertNil(t, err)

	assertEqual(t, wantRoom.Owner, roomData.Owner)
	assertEqual(t, wantRoom.MaxPlayers, roomData.MaxPlayers)
	assertEqual(t, true, len(roomData.ID) == 5)
}

func assertRegister(t *testing.T, cli *client.Client, lobby *quiz.Lobby, username string) {
	t.Helper()

	res, err := cli.Register(username)
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeRegister, res.Type)

	registerData, err := api.DecodeJSON[api.RegisterResponseData](res.Data)
	assertNil(t, err)

	claims, err := lobby.CheckToken(defaultTestConfig, registerData.Token)
	assertNil(t, err)

	usernameClaim, ok := quiz.GetStringClaim(claims, "username")
	assertEqual(t, true, ok)
	assertEqual(t, username, usernameClaim)
}

func assertLogin(t *testing.T, cli *client.Client, token string) {
	t.Helper()
	res, err := cli.Login(token)
	assertNil(t, err)
	assertEqual(t, api.ResponseTypeLogin, res.Type)
}

func assertLobbyUpdate(t *testing.T, cli *client.Client, username, action string) {
	t.Helper()

	res, err := cli.ReadResponse()
	assertNil(t, err)

	lobbyUpdateData, err := api.DecodeJSON[api.LobbyUpdateResponseData](res.Data)
	assertNil(t, err)

	assertEqual(t, res.Type, api.ResponseTypeLobbyUpdate)
	assertEqual(t, lobbyUpdateData.Username, username)
	assertEqual(t, lobbyUpdateData.Action, action)
}

func compactJSON(src []byte) ([]byte, error) {
	var buf bytes.Buffer
	if err := json.Compact(&buf, src); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func compactOrMarshalJSON(v any) ([]byte, error) {
	switch t := v.(type) {
	case []byte:
		return compactJSON(t)
	default:
		return json.Marshal(t)
	}
}

func assertEqualJSON(t *testing.T, want, got any) {
	t.Helper()

	wantBytes, err := compactOrMarshalJSON(want)
	if err != nil {
		t.Errorf("%v", err)
		return
	}
	gotBytes, err := compactOrMarshalJSON(got)
	if err != nil {
		t.Errorf("%v", err)
		return
	}

	if !bytes.Equal(wantBytes, gotBytes) {
		t.Errorf("assert equal json: got %s, want %s", gotBytes, wantBytes)
	}
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
		t.Errorf("assert nil: got %v", got)
	}
}

func assertNotNil(t *testing.T, got interface{}) {
	t.Helper()
	if got == nil || reflect.ValueOf(got).IsNil() {
		t.Fatalf("assert not nil: got %v", got)
	}
}
