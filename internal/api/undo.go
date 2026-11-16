package api

import (
	"sync"

	"ssss/internal/library"
)

// maxHistory bounds the undo stack; each entry can pin removed records.
const maxHistory = 200

type historyEntry struct {
	desc string
	op   library.Op
}

// history is the undo and redo stacks of one session. They die with the
// session: the ops are closures over its Library.
type history struct {
	mu   sync.Mutex
	undo []historyEntry
	redo []historyEntry
}

// did records a new mutation, which invalidates anything undone before it.
func (h *history) did(desc string, op library.Op) {
	if op == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.undo = append(h.undo, historyEntry{desc, op})
	if len(h.undo) > maxHistory {
		h.undo = h.undo[len(h.undo)-maxHistory:]
	}
	h.redo = nil
}

func pop(stack *[]historyEntry) (historyEntry, bool) {
	n := len(*stack)
	if n == 0 {
		return historyEntry{}, false
	}
	e := (*stack)[n-1]
	*stack = (*stack)[:n-1]
	return e, true
}

// step runs the newest entry of from and files its inverse under to.
func (a *API) step(s *session, from, to *[]historyEntry) (string, error) {
	s.history.mu.Lock()
	entry, ok := pop(from)
	s.history.mu.Unlock()
	if !ok {
		return "", nil
	}
	ch, err := entry.op()
	if err != nil {
		// The entry is spent either way: the library rolled the attempt
		// back, and retrying it would fail the same way.
		return "", err
	}
	a.commit(s, ch)
	if ch.Undo != nil {
		s.history.mu.Lock()
		*to = append(*to, historyEntry{entry.desc, ch.Undo})
		s.history.mu.Unlock()
	}
	return entry.desc, nil
}

// Undo reverts the last mutation and returns its description, or "" when
// there is nothing to undo.
func (a *API) Undo() (string, error) {
	s, err := a.writable()
	if err != nil {
		return "", err
	}
	return a.step(s, &s.history.undo, &s.history.redo)
}

// Redo reapplies the last undone mutation and returns its description, or ""
// when there is nothing to redo.
func (a *API) Redo() (string, error) {
	s, err := a.writable()
	if err != nil {
		return "", err
	}
	return a.step(s, &s.history.redo, &s.history.undo)
}
