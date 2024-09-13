package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/golang-jwt/jwt"
	"github.com/gorilla/websocket"
	"github.com/lithammer/shortuuid/v3"
)

type client struct {
	// Username is unique and used to define the lobby owner.
	Username string

	// Registered defines if a client registered a username.
	Registered bool
}

type lobbyState int

const (
	lobbyStateCreated lobbyState = iota
	lobbyStateRegister
	lobbyStateQuiz
	lobbyStateResponses
	lobbyStateEnded
)

type lobby struct {
	ID      string    `json:"id"`
	Created time.Time `json:"created"`
	Owner   string    `json:"owner"`

	// tokenValidity invalidates an access token if the "token_validity" claim
	// doesn't match. Since lobby ids are short-sized, it prevents previous
	// lobby owner/players from accessing a newly created lobby with the old token.
	tokenValidity string
	mu            sync.Mutex
	state         lobbyState
	conns         map[*websocket.Conn]client
}

func (l *lobby) setState(state lobbyState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

func (l *lobby) setConn(conn *websocket.Conn, client client) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.conns[conn] = client
}

var lobbies = map[string]*lobby{}
var lobbiesMu sync.Mutex

func addLobby(id string, lobby *lobby) {
	lobbiesMu.Lock()
	defer lobbiesMu.Unlock()
	lobbies[id] = lobby
}

type createLobbyResponse struct {
	LobbyID string `json:"lobby_id"`
	Path    string `json:"path"`
	Token   string `json:"token"`
}

func checkUsername(username string) error {
	if username == "" {
		return errors.New("missing username")
	}
	if utf8.RuneCountInString(username) > 25 {
		return errors.New("username too long")
	}

	return nil
}

var createLobbyHandler = func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	username := r.URL.Query().Get("username")
	if err := checkUsername(username); err != nil {
		apiErr := apiErrorResponse{err.Error()}
		writeJSONResponse(w, http.StatusBadRequest, apiErr)

		return
	}

	var lobbyID, tokenValidity string

	for {
		lobbyID = shortuuid.New()
		if len(lobbyID) < 5 {
			log.Println("generated id too short", lobbyID)
			w.WriteHeader(http.StatusInternalServerError)

			return
		}

		lobbyID = lobbyID[:5]
		tokenValidity = shortuuid.New()

		if _, exist := lobbies[lobbyID]; !exist {
			addLobby(lobbyID, &lobby{
				ID:            lobbyID,
				Created:       time.Now(),
				Owner:         username,
				tokenValidity: tokenValidity,
				state:         lobbyStateCreated,
				conns:         map[*websocket.Conn]client{},
			})

			break
		}
	}

	// TODO: invalidate token on lobby deletion.
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"lobby_id":       lobbyID,
		"token_validity": tokenValidity,
		"username":       username,
	})

	tokenStr, err := token.SignedString(jwtSecret)
	if err != nil {
		log.Println(err)
		delete(lobbies, lobbyID)
		w.WriteHeader(http.StatusInternalServerError)

		return
	}

	res := createLobbyResponse{
		LobbyID: lobbyID,
		Path:    "/lobby/" + lobbyID,
		Token:   tokenStr,
	}

	if err := json.NewEncoder(w).Encode(res); err != nil {
		log.Println(err)
	}
}

type userRegisterRequest struct {
	Username string `json:"username"`
}

type userRegisterResponse struct {
	Username   string `json:"username"`
	Registered bool   `json:"registered"`
	Token      string `json:"access_token"`
}

var lobbyHandler = func(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")

	id := r.PathValue("id")
	if id == "" {
		apiErr := apiErrorResponse{"missing id"}
		writeJSONResponse(w, http.StatusBadRequest, apiErr)

		return
	}

	lobby, exist := lobbies[id]
	if !exist {
		apiErr := apiErrorResponse{"lobby does not exist"}
		writeJSONResponse(w, http.StatusNotFound, apiErr)

		return
	}

	conn, err := defaultUpgrader.Upgrade(w, r, nil)
	if err != nil {
		// Upgrade already writes a status code and error message.
		log.Println(err)

		return
	}

	// Transition to the register state only after a first
	// call to the handler.
	if len(lobby.conns) == 0 {
		lobby.setState(lobbyStateRegister)
	}

	claims, valid, err := lobby.checkTokenFromRequest(r)
	if err != nil {
		log.Println(err)
	}

	var restitute bool

	if valid {
		// Restitute client by overriding old conn
		var username string

		usernameClaim, ok := claims["username"]
		if ok {
			username, ok = usernameClaim.(string)
		}

		if ok && username != "" {
			var oldConn *websocket.Conn
			var client client

			for oldConn, client = range lobby.conns {
				if client.Username == username {
					break
				}
			}

			// Close old connection to avoid network leaks.
			if oldConn != nil {
				oldConn.Close()
			}

			// Restitute client to new websocket conn.
			if client.Username != "" {
				restitute = true
				lobby.setConn(conn, client)
			}
		}
	}

	if !restitute {
		lobby.setConn(conn, client{
			Username:   "",
			Registered: false,
		})
	}

	for {
		if lobby.conns[conn].Registered {
			time.Sleep(5 * time.Second)

			continue
		}

		req := userRegisterRequest{}
		if err := conn.ReadJSON(&req); err != nil {
			log.Println(err)

			apiErr := apiErrorResponse{"invalid json request"}
			conn.WriteJSON(apiErr)

			continue
		}

		if err := checkUsername(req.Username); err != nil {
			apiErr := apiErrorResponse{err.Error()}
			writeJSONResponse(w, http.StatusBadRequest, apiErr)

			return
		}

		token := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
			"lobby_id":       lobby.ID,
			"token_validity": lobby.tokenValidity,
			"username":       req.Username,
		})

		tokenString, err := token.SignedString(jwtSecret)
		if err != nil {
			log.Println(err)

			apiErr := apiErrorResponse{"internal error: could not register username"}
			conn.WriteJSON(apiErr)

			return
		}

		lobby.setConn(conn, client{
			Username:   req.Username,
			Registered: true,
		})

		// There are no cookies sent after the websocket upgrade.
		// Communicate the new token via json instead.
		res := userRegisterResponse{
			Username:   req.Username,
			Registered: true,
			Token:      tokenString,
		}

		conn.WriteJSON(res)

		// TODO: on start, goto next phase
	}
}

func (l *lobby) checkTokenFromRequest(r *http.Request) (claims jwt.MapClaims, valid bool, err error) {
	cookie, err := r.Cookie("access_token")
	if err != nil {
		if errors.Is(err, http.ErrNoCookie) {
			return nil, false, nil
		}

		return nil, false, err
	}

	if cookie == nil {
		return nil, false, nil
	}

	return l.checkToken(cookie.Value)
}

func (l *lobby) checkToken(token string) (claims jwt.MapClaims, valid bool, err error) {
	jwtToken, err := jwt.Parse(token, func(token *jwt.Token) (any, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		return jwtSecret, nil
	})
	if err != nil {
		return nil, false, err
	}

	claims, ok := jwtToken.Claims.(jwt.MapClaims)
	if !ok {
		return nil, false, errors.New("could not assert jwt claims")
	}

	tokenValidity, ok := claims["token_validity"]
	if !ok {
		return nil, false, errors.New("token has no token_validity claim")
	}

	tokenValidityStr, ok := tokenValidity.(string)
	if !ok {
		return nil, false, errors.New("could not assert token validity to string")
	}

	if tokenValidityStr != l.tokenValidity {
		return nil, false, errors.New("token does not match token validity")
	}

	return claims, true, nil
}
