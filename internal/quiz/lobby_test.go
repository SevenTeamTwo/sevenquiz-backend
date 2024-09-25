package quiz_test

import (
	"embed"
	"io/fs"
	"reflect"
	"sevenquiz-backend/internal/quiz"
	"slices"
	"testing"
)

//go:embed tests/quizzes
var quizzes embed.FS

func TestListQuizzes(t *testing.T) {
	lobbies := &quiz.Lobbies{}
	quizzesFS, err := fs.Sub(quizzes, "tests/quizzes")
	assertNil(t, err)

	lobby, err := lobbies.Register(quiz.LobbyOptions{
		Quizzes: quizzesFS,
	})
	assertNil(t, err)

	gotList, err := lobby.ListQuizzes()
	assertNil(t, err)

	wantList := []string{"cars", "custom", "default"}
	assertEqualSlices(t, wantList, gotList)
}

func assertEqualSlices[T comparable](t *testing.T, want, got []T) {
	t.Helper()
	if !slices.Equal(want, got) {
		t.Errorf("assert equal: got %v, want %v", got, want)
	}
}

func assertNil(t *testing.T, got interface{}) {
	t.Helper()
	if !(got == nil || reflect.ValueOf(got).IsNil()) {
		t.Fatalf("assert nil: got %v", got)
	}
}
