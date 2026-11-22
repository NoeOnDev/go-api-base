package storage

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"time"
)

// Local keeps the objects as files under a directory (STORAGE_DIR). It is for development and for
// a single server: with several instances of the API each one would have its own disk, so they need
// a volume shared by all of them, or S3.
type Local struct {
	// root confines every path to the directory: a key cannot leave it, not even through a symbolic
	// link.
	root *os.Root
}

// NewLocal opens the directory, creating it if it does not exist.
func NewLocal(dir string) (*Local, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", dir, err)
	}
	if err := root.MkdirAll(tmpDir, 0o750); err != nil {
		return nil, fmt.Errorf("storage: create %s in %s: %w", tmpDir, dir, err)
	}
	l := &Local{root: root}
	l.sweep(time.Now().Add(-tmpMaxAge))
	return l, nil
}

const (
	// tmpDir is where a file is written before it takes its key. It starts with a dot: no key does.
	tmpDir = ".tmp"
	// tmpMaxAge is how old a temporary file must be for the startup to delete it: far more than an
	// upload takes, so another instance that shares the directory is not writing it.
	tmpMaxAge = 24 * time.Hour
)

// sweep deletes the temporary files a process left when it died in the middle of a Put: no row
// knows their names, so nothing else would. It is best effort: what it cannot delete stays.
func (l *Local) sweep(before time.Time) {
	entries, err := fs.ReadDir(l.root.FS(), tmpDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if info, err := e.Info(); err == nil && !e.IsDir() && info.ModTime().Before(before) {
			_ = l.root.Remove(path.Join(tmpDir, e.Name()))
		}
	}
}

// Put writes to a temporary file and renames it: a reader never sees half a file, and an upload cut
// in the middle leaves nothing under the key. If the process dies before the rename, the temporary
// file stays in tmpDir, and a later startup deletes it.
func (l *Local) Put(_ context.Context, key string, body io.ReadSeeker, _ int64, _ string) error {
	if dir := path.Dir(key); dir != "." {
		if err := l.root.MkdirAll(dir, 0o750); err != nil {
			return fmt.Errorf("storage: create the directory of %s: %w", key, err)
		}
	}
	tmp := path.Join(tmpDir, rand.Text())
	f, err := l.root.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
	if err != nil {
		return fmt.Errorf("storage: create %s: %w", key, err)
	}
	_, err = io.Copy(f, body)
	if err == nil {
		err = f.Sync() // on disk before the rename: a power cut does not leave an empty file
	}
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = l.root.Rename(tmp, key)
	}
	if err != nil {
		_ = l.root.Remove(tmp)
		return fmt.Errorf("storage: write %s: %w", key, err)
	}
	return nil
}

// Open returns the file, which is also an io.Seeker.
func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	f, err := l.root.Open(key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("storage: open %s: %w", key, err)
	}
	return f, nil
}

// Delete removes the file. The directories are left: they are by month, and few.
func (l *Local) Delete(_ context.Context, key string) error {
	if err := l.root.Remove(key); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("storage: delete %s: %w", key, err)
	}
	return nil
}
