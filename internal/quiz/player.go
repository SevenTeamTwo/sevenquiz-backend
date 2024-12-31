package quiz

import (
	"sevenquiz-backend/api"
	"sync"
)

// Player represents a quiz player.
//
// Multiple goroutines may invoke methods on a Player simultaneously.
type Player struct {
	username string
	answers  map[string]api.AnswerData
	alive    bool
	mu       sync.Mutex
}

func (c *Player) Username() string {
	return c.username
}

func (c *Player) Disconnect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alive = false
}

func (c *Player) Alive() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.alive
}

func (c *Player) Connect() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.alive = true
}

func (c *Player) RegisterAnswer(questionID string, answer api.AnswerData) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.answers[questionID] = answer
}
