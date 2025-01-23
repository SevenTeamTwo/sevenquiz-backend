package quiz

import (
	"iter"
	"sevenquiz-backend/api"
	"sync"
)

// Player represents a quiz player.
//
// Multiple goroutines may invoke methods on a Player simultaneously.
type Player struct {
	username string
	answers  map[int]api.Answer
	alive    bool
	mu       sync.RWMutex
}

func (p *Player) AllAnswers() iter.Seq2[int, api.Answer] {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return func(yield func(int, api.Answer) bool) {
		for i, answer := range p.answers {
			if !yield(i, answer) {
				return
			}
		}
	}
}

func (p *Player) Username() string {
	return p.username
}

func (p *Player) Disconnect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alive = false
}

func (p *Player) Alive() bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.alive
}

func (p *Player) Connect() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.alive = true
}

func (p *Player) RegisterAnswer(questionID int, answer api.Answer) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.answers == nil {
		p.answers = map[int]api.Answer{}
	}
	p.answers[questionID] = answer
}
