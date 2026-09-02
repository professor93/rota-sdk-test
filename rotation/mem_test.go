package rotation_test

import (
	"errors"
	"sync"
)

// memBackend is the smallest store.Backend: bytes in memory, a mutex for
// the lock, and counters so a test can prove a save did or did not happen.
// failOn forces an error at "load", "save" or "lock".
type memBackend struct {
	mu     sync.Mutex
	blob   []byte
	home   string
	loads  int
	saves  int
	locks  int
	failOn string
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
