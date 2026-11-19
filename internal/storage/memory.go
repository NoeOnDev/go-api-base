package storage

import (
	"bytes"
	"context"
	"io"
	"sync"
)

// Memory keeps the objects in memory. It is for the tests (testutil.NewAPI uses one), like
// mail.Recorder for the emails: everything is lost when the process ends.
type Memory struct {
	mu      sync.Mutex
	objects map[string][]byte
}

// NewMemory creates an empty store.
func NewMemory() *Memory {
	return &Memory{objects: map[string][]byte{}}
}

func (m *Memory) Put(_ context.Context, key string, body io.ReadSeeker, _ int64, _ string) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = data
	return nil
}

// Open returns a reader that is also an io.Seeker, like a file on disk.
func (m *Memory) Open(_ context.Context, key string) (io.ReadCloser, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	data, ok := m.objects[key]
	if !ok {
		return nil, ErrNotFound
	}
	return readSeekNopCloser{bytes.NewReader(data)}, nil
}

func (m *Memory) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.objects, key)
	return nil
}

// Keys returns the keys stored, for a test to check what is left.
func (m *Memory) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for k := range m.objects {
		keys = append(keys, k)
	}
	return keys
}

type readSeekNopCloser struct{ *bytes.Reader }

func (readSeekNopCloser) Close() error { return nil }
