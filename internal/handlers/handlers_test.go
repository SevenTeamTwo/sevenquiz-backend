package handlers_test

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sevenquiz-backend/api"
	"sevenquiz-backend/internal/client"
	"sevenquiz-backend/internal/config"
	"sevenquiz-backend/internal/handlers"
	mws "sevenquiz-backend/internal/middlewares"
	"sevenquiz-backend/internal/quiz"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

//go:embed tests/quizzes
var quizzes embed.FS

func getQuizzesFromFS(quizFS fs.FS) (map[string]api.Quiz, error) {
	quizzes := map[string]api.Quiz{}

	root := "."
	depth := 0

	err := fs.WalkDir(quizFS, root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if path == root {
			return nil
		}
		if d.IsDir() && strings.Count(path, "/") <= depth {
			path := d.Name() + "/questions.yml"
			f, err := quizFS.Open(path)
			if err != nil {
				return err
			}
			quiz := api.Quiz{Name: d.Name()}
			dec := yaml.NewDecoder(f)
			for {
				var q api.Question
				if err := dec.Decode(&q); err != nil {
					if errors.Is(err, io.EOF) {
						break
					}
					quiz.Questions = []api.Question{}
					return err
				}
				quiz.Questions = append(quiz.Questions, q)
			}
			quizzes[quiz.Name] = quiz
		}
		return nil
	})

	return quizzes, err
}

func init() {
	log.SetOutput(io.Discard)

	var err error
	quizzesFS, err := fs.Sub(quizzes, "tests/quizzes")
	if err != nil {
		log.Fatal(err)
	}
	quizzes, err := getQuizzesFromFS(quizzesFS)
	if err != nil {
		log.Fatal(err)
	}

	defaultTestLobbyOptions.Quizzes = quizzes
}

var (
	defaultTestConfig = config.Config{
		JWTSecret: []byte("myjwtsecret1234"),
		Lobby: config.LobbyConf{
			MaxPlayers:         20,
			RegisterTimeout:    15 * time.Second,
			WebsocketReadLimit: 512,
		},
	}
	defaultTestAcceptOptions = websocket.AcceptOptions{
		InsecureSkipVerify: true,
	}
	defaultTestWantLobby = api.LobbyResponseData{
		MaxPlayers:  20,
		Quizzes:     []string{"cars", "custom", "default"},
		CurrentQuiz: "cars",
	}
	defaultTestLobbyOptions = quiz.LobbyOptions{
		MaxPlayers: 20,
	}
)

// param named "_pattern" to avoid unparam linter FP until new pattern is tested.
func mustCreateAndDialTestServer(t *testing.T, _pattern string, handler http.Handler, path string) (*httptest.Server, *client.Client, *http.Response) {
	t.Helper()

	s := newTestServer(_pattern, handler)
	cli, res := mustDialTestServer(t, s, path)

	t.Cleanup(func() {
		s.Close()
	})

	return s, cli, res
}

func newTestServer(pattern string, handler http.Handler) *httptest.Server {
	mux := http.NewServeMux()
	mux.Handle(pattern, handler)
	return httptest.NewServer(mux)
}

func mustDialTestServer(t *testing.T, s *httptest.Server, path string) (*client.Client, *http.Response) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(s.URL, "http") + path
	cli, res, err := client.Dial(context.Background(), url, nil)
	if err != nil {
		t.Fatalf("Error while dialing test server: %v", err)
	}

	t.Cleanup(func() {
		cli.Close()
	})

	return cli, res
}

func TestLobbyCreate(t *testing.T) {
	var (
		lobbies = quiz.NewLobbiesCache()
		req     = httptest.NewRequest(http.MethodPost, "/lobby", nil)
		res     = httptest.NewRecorder()
	)

	if got, want := runtime.NumGoroutine(), 2; got != want {
		t.Errorf("Invalid amount of base goroutines, got %d, want %d", got, want)
	}

	// Should spawn a goroutine for lobby timeout.
	handlers.CreateLobbyHandler(defaultTestConfig, lobbies, defaultTestLobbyOptions.Quizzes)(res, req)

	if got, want := runtime.NumGoroutine(), 3; got != want {
		t.Error("Lobby's timeout goroutine did not spawn")
	}

	httpRes := res.Result()
	defer httpRes.Body.Close()

	if got, want := httpRes.StatusCode, http.StatusOK; got != want {
		t.Fatalf("CreateLobbyHandler returned unexpected status code, got %d, want %d", got, want)
	}

	apiRes := api.CreateLobbyResponseData{}
	err := json.NewDecoder(res.Body).Decode(&apiRes)
	if err != nil {
		t.Fatalf("Unexpected error while decoding create lobby response: %v", err)
	}

	lobby, ok := lobbies.Get(apiRes.LobbyID)
	if !ok || lobby == nil {
		t.Fatal("Could not get created lobby")
	}
	lobbyID := lobby.ID()
	if len(lobbyID) != 5 {
		t.Errorf("Unexpected lobby id in lobby banner: %s", lobbyID)
	}

	lobbies.Delete(lobbyID)
	<-time.After(time.Millisecond)

	if got, want := runtime.NumGoroutine(), 2; got != want {
		t.Errorf("Lobby's timeout goroutine was not cleaned up")
	}
}

func TestLobbyBanner(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, quiz.LobbyOptions{
			MaxPlayers: 20,
			Quizzes:    defaultTestLobbyOptions.Quizzes,
		})
		mw      = mws.NewLobby(lobbies)
		handler = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	_, cli, res := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	apiRes, err := cli.ReadResponse()
	if err != nil {
		t.Fatalf("Could not read lobby banner: %v", err)
	}

	if apiRes.Type != api.ResponseTypeLobby {
		t.Fatalf("Could not read lobby banner: got api response: %+v", res)
	}

	data, err := api.DecodeJSON[api.LobbyResponseData](apiRes.Data)
	if err != nil {
		t.Fatalf("Could not decode lobby data: %v", err)
	}

	want := defaultTestWantLobby

	if got, want := data.Owner, want.Owner; !cmp.Equal(got, want) {
		t.Errorf("Unexpected owner in lobby banner: got %v, want %v", got, want)
	}
	if got, want := data.MaxPlayers, want.MaxPlayers; got != want {
		t.Errorf("Unexpected max players in lobby banner: got %d, want %d", got, want)
	}
	if len(data.ID) != 5 {
		t.Errorf("Unexpected lobby id in lobby banner: %s", data.ID)
	}
	if data.Created == "" {
		t.Error("Missing created field in lobby banner")
	}
	if diff := cmp.Diff(want.Quizzes, data.Quizzes); diff != "" {
		t.Errorf("Unexpected quizzes list in lobby banner (-want+got):\n%v", diff)
	}
	if got, want := data.CurrentQuiz, want.CurrentQuiz; got != want {
		t.Errorf("Unexpected current quiz in lobby banner: got %s, want %s", got, want)
	}
}

func TestLobbyRegister(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, defaultTestLobbyOptions)
		mw             = mws.NewLobby(lobbies)
		handler        = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	_, cli, _ := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	username := "testuser"
	want := defaultTestWantLobby

	mustRegisterOwner(t, cli, &want, username)

	_, player, ok := lobby.GetPlayer(username)
	if !ok || player == nil {
		t.Fatalf("Could not get registered player")
	}
	if got, want := player.Username(), username; got != want {
		t.Errorf("Invalid player username, got %s, want %s", got, want)
	}

	res, err := cli.Register(username)
	if err != nil {
		t.Fatalf("Error while sending register command: %v", err)
	}
	if res.Type != api.ResponseTypeError {
		t.Fatalf("Unexpected api response: %+v", res)
	}

	data, err := api.DecodeJSON[api.ErrorData[api.WebsocketErrorCode]](res.Data)
	if err != nil {
		t.Fatalf("Error while decoding register response: %v", err)
	}
	if got, want := data.Code, api.PlayerAlreadyRegisteredCode; got != want {
		t.Errorf("Invalid register error code, got %d, want %d", got, want)
	}
}

func TestLobbyTimeout(t *testing.T) {
	t.Parallel()

	var (
		lobbies = quiz.NewLobbiesCache()
		req     = httptest.NewRequest(http.MethodPost, "/lobby", nil)
		res     = httptest.NewRecorder()
	)

	cfg := defaultTestConfig
	cfg.Lobby.RegisterTimeout = time.Nanosecond

	handlers.CreateLobbyHandler(cfg, lobbies, defaultTestLobbyOptions.Quizzes)(res, req)

	apiRes := &api.CreateLobbyResponseData{}
	if err := json.NewDecoder(res.Body).Decode(apiRes); err != nil {
		t.Fatalf("Could not decode create lobby response: %v", err)
	}

	// wait for the goroutine to process the delete
	time.Sleep(time.Millisecond)

	if lobby, ok := lobbies.Get(apiRes.LobbyID); ok || lobby != nil {
		t.Error("Lobby was not deleted after timeout")
	}
}

func TestLobbyPlayerList(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, defaultTestLobbyOptions)
		mw             = mws.NewLobby(lobbies)
		handler        = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	s, cli, _ := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	// Setup lobby owner
	owner := "owner"
	want := defaultTestWantLobby
	mustRegisterOwner(t, cli, &want, owner)

	players := map[string]*client.Client{
		"testuser":  nil,
		"testuser2": nil,
		"testuser3": nil,
	}

	for username := range players {
		cli2, _ := mustDialTestServer(t, s, path)
		players[username] = cli2

		mustRegisterPlayer(t, cli2, &want, username)
		mustBroadcastPlayerUpdate(t, cli, username, "join")

		want.PlayerList = append(want.PlayerList, username)
	}

	// Make sure new players are now in the list.
	mustLobby(t, cli, want)

	for username, cli2 := range players {
		cli2.Close()
		<-time.After(time.Millisecond)
		mustBroadcastPlayerUpdate(t, cli, username, "disconnect")

		// Make sure disconnected player is not on the list.
		want.PlayerList = slices.DeleteFunc(want.PlayerList, func(s string) bool {
			return s == username
		})
		mustLobby(t, cli, want)
	}
}

func TestLobbyMaxPlayers(t *testing.T) {
	t.Parallel()

	var (
		maxPlayers     = 1
		lobbies, lobby = mustRegisterLobby(t, quiz.LobbyOptions{
			MaxPlayers: maxPlayers,
			Quizzes:    defaultTestLobbyOptions.Quizzes,
		})
	)

	cfg := defaultTestConfig
	cfg.Lobby.MaxPlayers = maxPlayers

	mw := mws.NewLobby(lobbies)
	handler := handlers.LobbyHandler{
		Config:        cfg,
		Lobbies:       lobbies,
		AcceptOptions: defaultTestAcceptOptions,
	}
	path := "/lobby/" + lobby.ID()
	s, cli, _ := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	// Setup lobby owner
	want := defaultTestWantLobby
	want.MaxPlayers = maxPlayers
	mustRegisterOwner(t, cli, &want, "owner")

	// Make sure no players can join and be upgraded to websocket.
	url := "ws" + strings.TrimPrefix(s.URL, "http") + path
	cli, res, err := client.Dial(context.Background(), url, nil)
	if cli != nil {
		cli.Close()
	}
	if err == nil {
		t.Errorf("Player was able to join a full lobby, response %+v", res)
	}
}

func TestLobbyOwnerElection(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, defaultTestLobbyOptions)
		mw             = mws.NewLobby(lobbies)
		handler        = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	s, cli, _ := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	// Setup lobby owner
	owner := "owner"
	wantLobby := defaultTestWantLobby
	mustRegisterOwner(t, cli, &wantLobby, owner)

	// Setup second player to join
	cli2, _ := mustDialTestServer(t, s, path)

	nextPlayer := "nextplayer"
	mustRegisterPlayer(t, cli2, &wantLobby, nextPlayer)

	// Close owner client, must be replaced by next player.
	cli.Close()
	mustBroadcastPlayerUpdate(t, cli2, owner, "disconnect")
	mustBroadcastPlayerUpdate(t, cli2, nextPlayer, "new owner")
	if got, want := lobby.Owner(), nextPlayer; got != want {
		t.Errorf("Invalid lobby owner, got %s, want %s", got, want)
	}

	// Close new owner client, no other players so lobby must be deleted.
	cli2.Close()
	<-time.After(time.Millisecond)

	if lobby, ok := lobbies.Get(lobby.ID()); ok || lobby != nil {
		t.Error("Lobby was not deleted after owner disconnect")
	}
}

func TestLobbyKick(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, defaultTestLobbyOptions)
		mw             = mws.NewLobby(lobbies)
		handler        = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	s, cli, _ := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	// Setup lobby owner
	owner := "owner"
	wantLobby := defaultTestWantLobby
	mustRegisterOwner(t, cli, &wantLobby, owner)

	// Setup second player to join
	player := "player"
	cli2, _ := mustDialTestServer(t, s, path)

	mustRegisterPlayer(t, cli2, &wantLobby, player)
	mustBroadcastPlayerUpdate(t, cli, player, "join")

	// Player is not owner, kick must not be possible.
	res, err := cli2.Kick(owner)
	if err != nil {
		t.Fatalf("Unexpected error while trying to kick %s: %v", owner, err)
	}
	if got, want := res.Type, api.ResponseTypeError; got != want {
		t.Errorf("Invalid kick command response, got %s, want %s, response %+v", got, want, res)
	}

	res, err = cli.Kick(player)
	if err != nil {
		t.Fatalf("Unexpected error while trying to kick %s: %v", player, err)
	}
	if got, want := res.Type, api.ResponseTypeKick; got != want {
		t.Errorf("Invalid kick command response, got %s, want %s, response %+v", got, want, res)
	}

	mustBroadcastPlayerUpdate(t, cli, player, "kick")
}

func TestLobbyConfigure(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, defaultTestLobbyOptions)
		mw             = mws.NewLobby(lobbies)
		handler        = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	_, cli, _ := mustCreateAndDialTestServer(t, "GET /lobby/{id}", mws.Chain(handler, mw), path)

	// Setup lobby owner
	owner := "owner"
	wantLobby := defaultTestWantLobby
	mustRegisterOwner(t, cli, &wantLobby, owner)

	res, err := cli.Configure(wantLobby.Quizzes[1])
	if err != nil {
		t.Fatalf("Error while sending configure command: %v", err)
	}
	if got, want := res.Type, api.ResponseTypeConfigure; got != want {
		t.Errorf("Invalid configure response type: got %s, want %s", got, want)
	}

	mustBroadcastConfigure(t, cli, wantLobby.Quizzes[1])

	if got, want := lobby.Quiz(), wantLobby.Quizzes[1]; got != want {
		t.Errorf("Invalid configured quiz in lobby: got %s, want %s", got, want)
	}
}

func TestLobbyPassword(t *testing.T) {
	t.Parallel()

	var (
		lobbies, lobby = mustRegisterLobby(t, quiz.LobbyOptions{
			Quizzes:  defaultTestLobbyOptions.Quizzes,
			Password: "1234",
		})
		middlewares = []mws.Middleware{mws.Subprotocols, mws.NewLobby(lobbies)}
		handler     = handlers.LobbyHandler{
			Config:        defaultTestConfig,
			Lobbies:       lobbies,
			AcceptOptions: defaultTestAcceptOptions,
		}
		path = "/lobby/" + lobby.ID()
	)

	s := newTestServer("GET /lobby/{id}", mws.Chain(handler, middlewares...))
	defer s.Close()

	url := "ws" + strings.TrimPrefix(s.URL, "http") + path
	cli, res, err := client.Dial(context.Background(), url, nil)
	if cli != nil {
		cli.Close()
	}
	if err == nil {
		t.Fatalf("Player was able to join a password protected lobby")
	}
	if got, want := res.StatusCode, http.StatusUnauthorized; got != want {
		t.Errorf("Unexpected status code during ws handshake: got %d, want %d", got, want)
	}

	url += "?p=1234"
	cli, res, err = client.Dial(context.Background(), url, nil)
	if cli != nil {
		cli.Close()
	}
	if err != nil {
		t.Fatalf("Player was not able to join lobby with password: %v", err)
	}
	if got, want := res.StatusCode, http.StatusSwitchingProtocols; got != want {
		t.Errorf("Unexpected status code during ws handshake: got %d, want %d", got, want)
	}
}

func mustRegisterLobby(t *testing.T, opts quiz.LobbyOptions) (quiz.LobbyRepository, *quiz.Lobby) {
	t.Helper()

	lobbies := quiz.NewLobbiesCache()
	lobby, err := lobbies.Register(opts)
	if err != nil {
		t.Fatalf("Could not register lobby: %v", err)
	}
	return lobbies, lobby
}

func mustLobby(t *testing.T, cli *client.Client, want api.LobbyResponseData) {
	t.Helper()

	res, err := cli.Lobby()
	if err != nil {
		t.Fatalf("Error while sending lobby command: %v", err)
	}
	if res.Type != api.ResponseTypeLobby {
		t.Fatalf("Could not read lobby response: got api response: %+v", res)
	}

	mustDecodeLobbyData(t, res, want)
}

func mustLobbyBanner(t *testing.T, cli *client.Client, want api.LobbyResponseData) {
	t.Helper()

	res, err := cli.ReadResponse()
	if err != nil {
		t.Fatalf("Could not read lobby banner: %v", err)
	}
	if res.Type != api.ResponseTypeLobby {
		t.Fatalf("Could not read lobby banner: got api response: %+v", res)
	}

	mustDecodeLobbyData(t, res, want)
}

func mustDecodeLobbyData(t *testing.T, res api.Response[json.RawMessage], want api.LobbyResponseData) {
	t.Helper()

	data, err := api.DecodeJSON[api.LobbyResponseData](res.Data)
	if err != nil {
		t.Fatalf("Could not decode lobby data: %v", data)
	}
	if got, want := data.Owner, want.Owner; !cmp.Equal(got, want) {
		t.Fatalf("Unexpected owner in lobby banner: got %v, want %v", got, want)
	}
	if got, want := data.MaxPlayers, want.MaxPlayers; got != want {
		t.Fatalf("Unexpected max players in lobby banner: got %d, want %d", got, want)
	}
	if len(data.ID) != 5 {
		t.Fatalf("Unexpected lobby id in lobby banner: %s", data.ID)
	}
	if data.Created == "" {
		t.Fatal("Missing created field in lobby banner")
	}
	if diff := cmp.Diff(want.Quizzes, data.Quizzes); diff != "" {
		t.Fatalf("Unexpected quizzes list in lobby banner (-want+got):\n%v", diff)
	}
	if got, want := data.CurrentQuiz, want.CurrentQuiz; got != want {
		t.Fatalf("Unexpected current quiz in lobby banner: got %s, want %s", got, want)
	}
}

func mustRegisterPlayer(t *testing.T, cli *client.Client, wantLobby *api.LobbyResponseData, username string) {
	t.Helper()

	mustLobbyBanner(t, cli, *wantLobby)
	mustRegister(t, cli, username)
	mustBroadcastPlayerUpdate(t, cli, username, "join")

	wantLobby.PlayerList = append(wantLobby.PlayerList, username)
}

func mustRegisterOwner(t *testing.T, cli *client.Client, wantLobby *api.LobbyResponseData, username string) {
	t.Helper()

	mustRegisterPlayer(t, cli, wantLobby, username)
	mustBroadcastPlayerUpdate(t, cli, username, "new owner")

	wantLobby.Owner = &username
}

func mustRegister(t *testing.T, cli *client.Client, username string) {
	t.Helper()

	res, err := cli.Register(username)
	if err != nil {
		t.Fatalf("Could not register username: %v", err)
	}
	if res.Type != api.ResponseTypeRegister {
		t.Fatalf("Could not register username: got api response: %+v", res)
	}
}

func mustBroadcastPlayerUpdate(t *testing.T, cli *client.Client, username, action string) {
	t.Helper()

	res, err := cli.ReadResponse()
	if err != nil {
		t.Fatalf("Could not read lobby update broadcast: %v", err)
	}
	if res.Type != api.ResponseTypePlayerUpdate {
		t.Fatalf("Could not read lobby update broadcast: got api response: %+v", res)
	}

	data, err := api.DecodeJSON[api.PlayerUpdateResponseData](res.Data)
	if err != nil {
		t.Fatalf("Could not decode lobby update broadcast data: %v", data)
	}
	if username != data.Username {
		t.Fatalf("Unexpected username returned in lobby update broadcast: %s", data.Username)
	}
	if action != data.Action {
		t.Fatalf("Unexpected action returned in lobby update broadcast: %s", data.Action)
	}
}

func mustBroadcastConfigure(t *testing.T, cli *client.Client, quiz string) {
	t.Helper()

	res, err := cli.ReadResponse()
	if err != nil {
		t.Fatalf("Could not read configure broadcast: %v", err)
	}
	if res.Type != api.ResponseTypeConfigure {
		t.Fatalf("Could not read configure broadcast: got api response: %+v", res)
	}

	data, err := api.DecodeJSON[api.LobbyUpdateResponseData](res.Data)
	if err != nil {
		t.Fatalf("Could not decode configure broadcast data: %v", data)
	}
	if quiz != data.Quiz {
		t.Fatalf("Unexpected quiz returned in configure broadcast: %s", data.Quiz)
	}
}
