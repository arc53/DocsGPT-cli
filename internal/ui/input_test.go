package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestFieldEditing(t *testing.T) {
	var f field
	steps := []struct {
		key  tea.KeyMsg
		want string
	}{
		{runes("hello world"), "hello world"},
		{key(tea.KeyCtrlW), "hello "},
		{key(tea.KeyLeft), "hello "},
		{key(tea.KeyBackspace), "hell "},
		{key(tea.KeyHome), "hell "},
		{runes(">"), ">hell "},
		{key(tea.KeyDelete), ">ell "},
		{key(tea.KeyEnd), ">ell "},
		{tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("a\r\nb\tc"), Paste: true}, ">ell ab c"},
		{key(tea.KeyCtrlA), ">ell ab c"},
		{key(tea.KeyCtrlK), ""},
		{runes("xy"), "xy"},
		{key(tea.KeyCtrlU), ""},
	}
	for i, s := range steps {
		f.update(s.key)
		if f.String() != s.want {
			t.Fatalf("step %d (%s): %q, want %q", i, s.key, f.String(), s.want)
		}
	}
}

func TestFieldScrollsToCursor(t *testing.T) {
	var f field
	f.set(strings.Repeat("abcdefghij", 5) + "END")
	v := ansi.Strip(f.view(10, false, ""))
	if ansi.StringWidth(v) > 10 || !strings.Contains(v, "END") {
		t.Fatalf("view = %q", v)
	}
	f.pos = 0
	if v := ansi.Strip(f.view(10, false, "")); v != "abcdefghij" {
		t.Fatalf("view at start = %q", v)
	}
}

func TestInputMask(t *testing.T) {
	m := newInputModel(Input{Title: "API key", Mask: true, Placeholder: "paste your key"})
	if !strings.Contains(plain(m), "> paste your key") {
		t.Fatalf("placeholder:\n%s", plain(m))
	}
	send(m, runes("secret"))
	v := plain(m)
	if strings.Contains(v, "secret") || !strings.Contains(v, "••••••") {
		t.Fatalf("masked view leaks the value:\n%s", v)
	}
	if !send(m, key(tea.KeyEnter)) || m.field.String() != "secret" {
		t.Fatal("enter without Validate should submit")
	}
	if got := plain(m); got != "API key: ••••••••\n" {
		t.Fatalf("summary = %q", got)
	}
}

func TestInputValidation(t *testing.T) {
	var seen []string
	m := newInputModel(Input{Title: "API key", Validate: func(_ context.Context, v string) error {
		seen = append(seen, v)
		if v != "good" {
			return errors.New("invalid key\n(401)")
		}
		return nil
	}})
	send(m, runes("bad"))
	_, cmd := m.Update(key(tea.KeyEnter))
	if !m.validating || !strings.Contains(plain(m), "Validating…") {
		t.Fatalf("expected the spinner:\n%s", plain(m))
	}
	send(m, runes("ignored"))
	if m.field.String() != "bad" {
		t.Fatal("typing while validating changed the value")
	}
	m.Update(runValidation(t, cmd))
	if m.done || !strings.Contains(plain(m), "✗ invalid key (401)") {
		t.Fatalf("expected an inline error:\n%s", plain(m))
	}
	send(m, key(tea.KeyCtrlU))
	if strings.Contains(plain(m), "invalid") {
		t.Fatal("editing should clear the error")
	}
	send(m, runes("good"))
	_, cmd = m.Update(key(tea.KeyEnter))
	_, cmd = m.Update(runValidation(t, cmd))
	if cmd == nil || !m.submitted || plain(m) != "API key: good\n" {
		t.Fatalf("success: submitted=%v view=%q", m.submitted, plain(m))
	}
	if strings.Join(seen, ",") != "bad,good" {
		t.Fatalf("validated %v", seen)
	}
}

func TestInputCancelStopsValidation(t *testing.T) {
	cancelled := make(chan struct{})
	m := newInputModel(Input{Validate: func(ctx context.Context, _ string) error {
		<-ctx.Done()
		close(cancelled)
		return ctx.Err()
	}})
	_, cmd := m.Update(key(tea.KeyEnter))
	go func() {
		batch := cmd().(tea.BatchMsg)
		batch[len(batch)-1]()
	}()
	if !send(m, key(tea.KeyEsc)) || m.submitted {
		t.Fatal("esc should cancel while validating")
	}
	select {
	case <-cancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("validation context was not cancelled")
	}
	if m.View() != " " {
		t.Fatalf("cancelled input left %q", m.View())
	}
}

// runValidation runs the validation command from an Enter batch and
// returns its result message.
func runValidation(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("enter did not start a validation batch")
	}
	// The validation command is the last one; the first is the spinner tick.
	return batch[len(batch)-1]()
}
