package docker

import (
	"context"
	"errors"
	"testing"
)

// A name that is taken is a sentinel callers can tell from a failure, and
// the words stay what they were.
func TestCreatingAProjectThatExistsIsErrProjectExists(t *testing.T) {
	m := NewComposeManager(t.TempDir(), nil)
	ctx := context.Background()
	if _, err := m.CreateProject(ctx, "proj", "services: {}\n"); err != nil {
		t.Fatal(err)
	}
	_, err := m.CreateProject(ctx, "proj", "services: {}\n")
	if !errors.Is(err, ErrProjectExists) {
		t.Fatalf("err = %v, want ErrProjectExists", err)
	}
	if got, want := err.Error(), `project "proj" already exists`; got != want {
		t.Errorf("message %q, want %q", got, want)
	}
}
