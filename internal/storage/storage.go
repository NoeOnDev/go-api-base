// Package storage keeps the bytes of the uploaded files: on the local disk (Local), in a bucket of
// S3 or of a compatible service (S3), or in memory for the tests (Memory). It is the equivalent of
// Laravel's Storage disks. It only knows keys and bytes: whose each file is, its name and its type
// are in the files table (see internal/files and docs/files.md).
package storage

import (
	"context"
	"errors"
	"io"
)

// ErrNotFound means there is no object with that key.
var ErrNotFound = errors.New("storage: object not found")

// Store keeps objects by key. A key is a relative path with "/" ("2026/11/3f9a…"), made by the
// code, never by the user.
type Store interface {
	// Put stores size bytes of body under key, replacing what was there. contentType is kept with the
	// object where the store has a place for it (S3). body is seekable because S3 reads it twice: to
	// sign it and to send it.
	Put(ctx context.Context, key string, body io.ReadSeeker, size int64, contentType string) error
	// Open returns the object, or ErrNotFound. The caller closes it. With a store on disk it is also
	// an io.Seeker, which lets http.ServeContent answer a part of the file (a Range request).
	Open(ctx context.Context, key string) (io.ReadCloser, error)
	// Delete removes the object. A key that does not exist is not an error: deleting twice is fine.
	Delete(ctx context.Context, key string) error
}
