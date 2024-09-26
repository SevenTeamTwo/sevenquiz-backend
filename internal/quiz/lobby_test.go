package quiz_test

import (
	"embed"
	"io/fs"
	"sevenquiz-backend/internal/quiz"
	"testing"

	"github.com/google/go-cmp/cmp"
)

//go:embed tests/quizzes
var quizzes embed.FS

func TestListQuizzes(t *testing.T) {
	lobbies := &quiz.Lobbies{}
	quizzesFS, err := fs.Sub(quizzes, "tests/quizzes")
	if err != nil {
		t.Fatalf("Could not subtree the embed test quizzes FS: %v", err)
	}

	lobby, err := lobbies.Register(quiz.LobbyOptions{
		Quizzes: quizzesFS,
	})
	if err != nil {
		t.Fatalf("Could not register lobby: %v", err)
	}

	want := []string{"cars", "custom", "default"}
	got, err := lobby.ListQuizzes()
	if err != nil {
		t.Fatalf("Could not list quizzes: %v", err)
	}
	if diff := cmp.Diff(got, want); diff != "" {
		t.Errorf("ListQuizzes() returned unexpected diff (-want+got):\n%v", diff)
	}
}
