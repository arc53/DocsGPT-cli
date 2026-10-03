package ui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func key(t tea.KeyType) tea.KeyMsg { return tea.KeyMsg{Type: t} }

// send feeds keys to a model and reports whether it asked to quit.
func send(m tea.Model, keys ...tea.KeyMsg) bool {
	quit := false
	for _, k := range keys {
		_, cmd := m.Update(k)
		if cmd != nil {
			_, quit = cmd().(tea.QuitMsg)
		}
	}
	return quit
}

func plain(m tea.Model) string { return ansi.Strip(m.View()) }

func approvalSelect() Select {
	return Select{
		Title:  "Run this command?",
		Inline: true,
		Items: []Item{
			{Label: "Approve", Keys: []string{"a"}, Value: "approve"},
			{Label: "Always allow", Keys: []string{"l", "A"}, Value: "always"},
			{Label: "Deny", Keys: []string{"d"}, Value: "deny"},
			{Label: "Edit", Keys: []string{"e"}, Value: "edit"},
		},
	}
}

func modelList(n int) []Item {
	items := make([]Item, n)
	for i := range items {
		items[i] = Item{Label: fmt.Sprintf("model-%02d", i+1), Description: "desc"}
	}
	return items
}

func TestSelectNavigateAndChoose(t *testing.T) {
	m := newSelectModel(Select{Title: "Key", Items: []Item{{Label: "a"}, {Label: "b", Value: "B"}, {Label: "c"}}, Default: 1})
	m.setSize(80, 24)
	if !strings.Contains(plain(m), "→ b") {
		t.Fatalf("default not selected:\n%s", plain(m))
	}
	send(m, key(tea.KeyUp), key(tea.KeyUp)) // wraps from the top to the last item
	if !strings.Contains(plain(m), "→ c") {
		t.Fatalf("up did not wrap:\n%s", plain(m))
	}
	send(m, key(tea.KeyDown), runes("j"))
	if !send(m, key(tea.KeyEnter)) || m.chosen.value() != "B" {
		t.Fatalf("chosen = %+v", m.chosen)
	}
	if got := plain(m); got != "Key: b\n" {
		t.Fatalf("summary = %q", got)
	}
}

func TestSelectShortcuts(t *testing.T) {
	for k, want := range map[string]string{"a": "approve", "A": "always", "l": "always", "d": "deny", "e": "edit"} {
		m := newSelectModel(approvalSelect())
		if !send(m, runes(k)) || m.chosen.value() != want {
			t.Errorf("key %q chose %+v, want %s", k, m.chosen, want)
		}
	}
	m := newSelectModel(approvalSelect())
	if send(m, runes("x"), runes("D")) || m.done {
		t.Fatal("an unbound key finished the prompt")
	}

	s := approvalSelect()
	s.Summary = func(it Item) string { return "✓ " + it.Label }
	m = newSelectModel(s)
	send(m, runes("a"))
	if got := plain(m); got != "✓ Approve\n" {
		t.Fatalf("custom summary = %q", got)
	}
	s.Summary = func(Item) string { return "" }
	m = newSelectModel(s)
	send(m, runes("a"))
	if got := m.View(); got != " " {
		t.Fatalf("empty summary should blank the frame, got %q", got)
	}
}

func TestSelectCancel(t *testing.T) {
	for _, k := range []tea.KeyMsg{key(tea.KeyEsc), key(tea.KeyCtrlC)} {
		m := newSelectModel(approvalSelect())
		if !send(m, k) || m.chosen != nil {
			t.Fatalf("%s did not cancel", k)
		}
		if m.View() != " " {
			t.Fatalf("cancelled prompt left %q", m.View())
		}
	}
}

func TestSelectInline(t *testing.T) {
	m := newSelectModel(approvalSelect())
	m.setSize(100, 24)
	lines := strings.Split(plain(m), "\n")
	if lines[0] != "Run this command?  → Approve    Always allow    Deny    Edit" {
		t.Fatalf("inline row = %q", lines[0])
	}
	send(m, key(tea.KeyRight), key(tea.KeyRight), key(tea.KeyLeft), key(tea.KeyTab))
	if !send(m, key(tea.KeyEnter)) || m.chosen.value() != "deny" {
		t.Fatalf("chosen = %+v", m.chosen)
	}

	// Too narrow for one row: the title moves up, then it becomes a list.
	m = newSelectModel(approvalSelect())
	m.setSize(50, 24)
	if lines := strings.Split(plain(m), "\n"); lines[0] != "Run this command?" || !strings.HasPrefix(lines[1], "→ Approve") {
		t.Fatalf("narrow inline:\n%s", plain(m))
	}
	m.setSize(20, 24)
	if !strings.Contains(plain(m), "\n  l Always allow") {
		t.Fatalf("expected list fallback:\n%s", plain(m))
	}
}

func TestSelectFilter(t *testing.T) {
	items := []Item{
		{Label: "gpt-4o", Description: "openai"},
		{Label: "gemini-2.5-pro", Description: "google"},
		{Label: "gemini-2.5-flash", Description: "google"},
		{Label: "command-r", Description: "cohere", Keys: []string{"c"}},
	}
	m := newSelectModel(Select{Title: "Model", Items: items, Filter: true})
	m.setSize(80, 24)
	if !strings.Contains(plain(m), "> type to filter") {
		t.Fatalf("no filter line:\n%s", plain(m))
	}
	send(m, runes("g"), runes("e"), runes("m"))
	v := plain(m)
	if strings.Contains(v, "gpt-4o") || !strings.Contains(v, "→ gemini-2.5-pro") || !strings.Contains(v, "esc clear") {
		t.Fatalf("filter gem:\n%s", v)
	}
	send(m, key(tea.KeyBackspace), key(tea.KeyBackspace), key(tea.KeyBackspace), runes("c"))
	if m.done || !strings.Contains(plain(m), "→ command-r") {
		t.Fatalf("a printable key bound to an item must filter, not choose:\n%s", plain(m))
	}
	send(m, runes("zz"))
	if !strings.Contains(plain(m), "No matches") {
		t.Fatalf("expected No matches:\n%s", plain(m))
	}
	if send(m, key(tea.KeyEnter)) {
		t.Fatal("enter with no matches finished the prompt")
	}
	if send(m, key(tea.KeyEsc)) || m.filter.String() != "" {
		t.Fatal("esc should clear a non-empty filter first")
	}
	if !strings.Contains(plain(m), "gpt-4o") {
		t.Fatalf("clearing the filter should restore the list:\n%s", plain(m))
	}
	if !send(m, key(tea.KeyEsc)) || m.chosen != nil {
		t.Fatal("esc on an empty filter should cancel")
	}

	// A description match counts, but ranks below label matches.
	got := filterItems(items, "google")
	if len(got) != 2 || items[got[0]].Description != "google" {
		t.Fatalf("description filter = %v", got)
	}
	if got := filterItems(items, "flash gem"); len(got) != 1 || items[got[0]].Label != "gemini-2.5-flash" {
		t.Fatalf("multi-token filter = %v", got)
	}
}

func TestSelectScroll(t *testing.T) {
	m := newSelectModel(Select{Items: modelList(14), Height: 5})
	m.setSize(80, 24)
	if v := plain(m); !strings.Contains(v, "(1/14)") || strings.Contains(v, "model-06") {
		t.Fatalf("initial window:\n%s", v)
	}
	for range 7 {
		send(m, key(tea.KeyDown))
	}
	v := plain(m)
	if !strings.Contains(v, "(8/14)") || !strings.Contains(v, "→ model-08") || strings.Count(v, "model-") != 5 {
		t.Fatalf("scrolled window:\n%s", v)
	}
	if !strings.Contains(v, "model-06") || !strings.Contains(v, "model-10") {
		t.Fatalf("window not centred on the cursor:\n%s", v)
	}
	send(m, key(tea.KeyPgDown), key(tea.KeyPgDown))
	if !strings.Contains(plain(m), "(14/14)") {
		t.Fatalf("pgdown:\n%s", plain(m))
	}
	send(m, key(tea.KeyHome))
	if !strings.Contains(plain(m), "(1/14)") {
		t.Fatalf("home:\n%s", plain(m))
	}

	// A short terminal shrinks the window; no indicator when everything fits.
	m.setSize(80, 8)
	if strings.Count(plain(m), "model-") != 3 {
		t.Fatalf("short terminal:\n%s", plain(m))
	}
	m = newSelectModel(Select{Items: modelList(3)})
	if strings.Contains(plain(m), "/3)") {
		t.Fatalf("unexpected scroll indicator:\n%s", plain(m))
	}
}

func TestSelectColumns(t *testing.T) {
	items := []Item{{Label: "support", Description: "Support agent"}, {Label: "a-much-longer-label", Description: "Other"}}
	m := newSelectModel(Select{Items: items})
	m.setSize(80, 24)
	lines := strings.Split(strings.ReplaceAll(plain(m), "→", ">"), "\n")
	if strings.Index(lines[0], "Support agent") != strings.Index(lines[1], "Other") {
		t.Fatalf("descriptions not aligned:\n%s", plain(m))
	}
	m.setSize(30, 24)
	for _, l := range strings.Split(plain(m), "\n") {
		if ansi.StringWidth(l) > 30 {
			t.Fatalf("line wider than the terminal: %q", l)
		}
	}
}

func TestConfirm(t *testing.T) {
	m := newSelectModel(confirmSelect("Delete agent?", false))
	if !send(m, key(tea.KeyEnter)) || m.chosen.value() != "No" {
		t.Fatalf("default no: %+v", m.chosen)
	}
	m = newSelectModel(confirmSelect("Delete agent?", false))
	send(m, runes("Y"))
	if m.chosen.value() != "Yes" || plain(m) != "Delete agent? Yes\n" {
		t.Fatalf("Y: %+v %q", m.chosen, plain(m))
	}
}

func TestNotInteractive(t *testing.T) {
	if Interactive() {
		t.Skip("running in a terminal")
	}
	if _, err := approvalSelect().Run(); err != ErrNotInteractive {
		t.Fatalf("Select err = %v", err)
	}
	if _, err := Confirm("ok?", true); err != ErrNotInteractive {
		t.Fatalf("Confirm err = %v", err)
	}
	if _, err := (Input{Title: "Name"}).Run(); err != ErrNotInteractive {
		t.Fatalf("Input err = %v", err)
	}
}

func TestInlineChoiceStopsAtEnds(t *testing.T) {
	m := newSelectModel(confirmSelect("Remove?", false))
	m.setSize(80, 24)
	send(m, key(tea.KeyRight))
	if m.cursor != 1 {
		t.Fatalf("right on the last choice moved to %d", m.cursor)
	}
	send(m, key(tea.KeyLeft), key(tea.KeyLeft))
	if m.cursor != 0 {
		t.Fatalf("left past the first choice moved to %d", m.cursor)
	}
}
