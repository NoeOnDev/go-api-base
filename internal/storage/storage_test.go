package storage_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NoeOnDev/go-api-base/internal/storage"
)

// contract checks what every Store does the same way.
func contract(t *testing.T, st storage.Store) {
	t.Helper()
	ctx := t.Context()
	const key = "2026/11/abc123"

	if _, err := st.Open(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("Open of a missing key: %v, want ErrNotFound", err)
	}

	put := func(content string) {
		t.Helper()
		if err := st.Put(ctx, key, strings.NewReader(content), int64(len(content)), "text/plain"); err != nil {
			t.Fatal(err)
		}
	}
	read := func() string {
		t.Helper()
		body, err := st.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		defer body.Close()
		data, err := io.ReadAll(body)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	put("hello")
	if got := read(); got != "hello" {
		t.Errorf("read %q, want hello", got)
	}
	put("replaced") // the same key again replaces it
	if got := read(); got != "replaced" {
		t.Errorf("read %q, want replaced", got)
	}

	// Deleting twice is fine: the cleanup can be cut between the bytes and the row.
	for range 2 {
		if err := st.Delete(ctx, key); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.Open(ctx, key); !errors.Is(err, storage.ErrNotFound) {
		t.Errorf("Open after Delete: %v, want ErrNotFound", err)
	}
}

func TestMemory(t *testing.T) {
	st := storage.NewMemory()
	contract(t, st)
	if keys := st.Keys(); len(keys) != 0 {
		t.Errorf("keys left: %v", keys)
	}
}

func TestLocal(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "storage") // it does not exist yet: NewLocal creates it
	st, err := storage.NewLocal(dir)
	if err != nil {
		t.Fatal(err)
	}
	contract(t, st)

	// A file on disk can be read from any point: http.ServeContent needs it for the Range requests.
	if err := st.Put(t.Context(), "a/b", strings.NewReader("0123456789"), 10, ""); err != nil {
		t.Fatal(err)
	}
	body, err := st.Open(t.Context(), "a/b")
	if err != nil {
		t.Fatal(err)
	}
	defer body.Close()
	if _, ok := body.(io.ReadSeeker); !ok {
		t.Error("the file of the local store is not an io.ReadSeeker")
	}
	// No temporary file is left next to it.
	entries, err := os.ReadDir(filepath.Join(dir, "a"))
	if err != nil || len(entries) != 1 {
		t.Errorf("entries = %v, %v; want only the file", entries, err)
	}

	// A key cannot leave the directory.
	outside := filepath.Join(filepath.Dir(dir), "outside")
	for _, key := range []string{"../outside", "/" + outside, "a/../../outside"} {
		if err := st.Put(t.Context(), key, strings.NewReader("x"), 1, ""); err == nil {
			t.Errorf("Put(%q) should fail", key)
		}
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a file was written outside the directory: %v", err)
	}
}

// fakeS3 is a server that answers like S3 for the three operations the store uses, with the bucket
// in the path. It checks that the SDK is used as a compatible service expects; a real one is tried
// with TestS3Real.
type fakeS3 struct {
	mu       sync.Mutex
	objects  map[string][]byte
	types    map[string]string
	unsigned int
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=test-key/") {
		f.unsigned++
	}
	key := r.URL.Path // "/bucket/2026/11/abc123"
	switch r.Method {
	case http.MethodPut:
		data, _ := io.ReadAll(r.Body)
		f.objects[key], f.types[key] = data, r.Header.Get("Content-Type")
	case http.MethodGet:
		data, ok := f.objects[key]
		if !ok {
			w.Header().Set("Content-Type", "application/xml")
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `<?xml version="1.0" encoding="UTF-8"?><Error><Code>NoSuchKey</Code><Message>The specified key does not exist.</Message></Error>`)
			return
		}
		_, _ = w.Write(data)
	case http.MethodDelete:
		delete(f.objects, key)
		w.WriteHeader(http.StatusNoContent)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func TestS3(t *testing.T) {
	fake := &fakeS3{objects: map[string][]byte{}, types: map[string]string{}}
	srv := httptest.NewServer(fake)
	defer srv.Close()

	st, err := storage.NewS3(t.Context(), storage.S3Config{
		Bucket: "bucket", Region: "us-east-1", Endpoint: srv.URL, PathStyle: true,
		AccessKeyID: "test-key", SecretAccessKey: "test-secret",
	})
	if err != nil {
		t.Fatal(err)
	}
	contract(t, st)

	if err := st.Put(t.Context(), "2026/11/photo", bytes.NewReader([]byte("png")), 3, "image/png"); err != nil {
		t.Fatal(err)
	}
	if got := fake.types["/bucket/2026/11/photo"]; got != "image/png" {
		t.Errorf("Content-Type stored = %q, want image/png", got)
	}
	if fake.unsigned > 0 {
		t.Errorf("%d requests were not signed with the configured key", fake.unsigned)
	}
}

// TestS3Real tries a real bucket: Amazon S3, or a compatible service like MinIO. It only runs with
// S3_TEST_BUCKET (and S3_TEST_REGION, S3_TEST_ENDPOINT, S3_TEST_ACCESS_KEY_ID,
// S3_TEST_SECRET_ACCESS_KEY and S3_TEST_PATH_STYLE=true as the service needs); see docs/files.md.
func TestS3Real(t *testing.T) {
	bucket := os.Getenv("S3_TEST_BUCKET")
	if bucket == "" {
		t.Skip("S3_TEST_BUCKET is not set: skipping the test against a real bucket")
	}
	st, err := storage.NewS3(context.Background(), storage.S3Config{
		Bucket: bucket, Region: os.Getenv("S3_TEST_REGION"), Endpoint: os.Getenv("S3_TEST_ENDPOINT"),
		AccessKeyID: os.Getenv("S3_TEST_ACCESS_KEY_ID"), SecretAccessKey: os.Getenv("S3_TEST_SECRET_ACCESS_KEY"),
		PathStyle: os.Getenv("S3_TEST_PATH_STYLE") == "true",
	})
	if err != nil {
		t.Fatal(err)
	}
	contract(t, st)
}
