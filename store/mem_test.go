package store_test

import (
	"errors"
	"sync"
)

// memBackend is the smallest store.Backend: a blob behind a mutex, with
// counters so a test can prove a save did or did not happen, and failOn to
// make one step fail. Two of them sharing a home stand in for two processes
// over one directory, because flock is per open file description and a
// FileBackend opened twice in one goroutine would block on itself.
type memBackend struct {
	mu     sync.Mutex
	blob   []byte
	home   string
	loads  int
	saves  int
	locks  int
	failOn string // load | save | lock
	onSave func() // runs after each successful Save
}

func (m *memBackend) Load() ([]byte, error) {
	m.loads++
	if m.failOn == "load" {
		return nil, errors.New("boom")
	}
	return m.blob, nil
}

func (m *memBackend) Save(b []byte) error {
	if m.failOn == "save" {
		return errors.New("boom")
	}
	m.saves++
	m.blob = append([]byte(nil), b...)
	if m.onSave != nil {
		m.onSave()
	}
	return nil
}

func (m *memBackend) Lock() (func(), error) {
	if m.failOn == "lock" {
		return nil, errors.New("boom")
	}
	m.locks++
	m.mu.Lock()
	return m.mu.Unlock, nil
}

func (m *memBackend) HomeRoot() string { return m.home }
