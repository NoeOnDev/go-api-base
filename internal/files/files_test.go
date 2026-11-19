package files_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/files"
	"github.com/NoeOnDev/go-api-base/internal/storage"
	"github.com/NoeOnDev/go-api-base/internal/store"
	"github.com/NoeOnDev/go-api-base/internal/testutil"
)

// These tests go through the whole API, with a store in memory instead of the disk or S3.

type file struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	URL         string `json:"url"`
}

type fixture struct {
	api               http.Handler
	pool              *pgxpool.Pool
	store             *storage.Memory
	admin, alice, bob *http.Cookie
	aliceID           int64
}

func setup(t *testing.T) fixture {
	t.Helper()
	pool := testutil.NewDB(t)
	f := fixture{pool: pool, store: storage.NewMemory()}
	f.api = testutil.NewAPI(pool, testutil.WithStorage(f.store))
	_, f.admin = testutil.Login(t, pool, "admin@example.com", auth.RoleAdmin)
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	f.alice, f.aliceID = cookie, alice.ID
	_, f.bob = testutil.Login(t, pool, "bob@example.com", auth.RoleUser)
	return f
}

func (f fixture) upload(t *testing.T, cookie *http.Cookie, name string, content []byte) file {
	t.Helper()
	return testutil.Decode[file](t, testutil.Upload(t, f.api, cookie, "/files", name, content), http.StatusCreated)
}

// get asks for a file with optional headers ("Name: value").
func (f fixture) get(cookie *http.Cookie, path string, headers ...string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodGet, path, nil)
	for _, h := range headers {
		name, value, _ := strings.Cut(h, ": ")
		r.Header.Set(name, value)
	}
	if cookie != nil {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	f.api.ServeHTTP(w, r)
	return w
}

func TestUploadAndDownload(t *testing.T) {
	f := setup(t)

	w := testutil.Upload(t, f.api, f.alice, "/files", "photo.png", testutil.PNG)
	up := testutil.Decode[file](t, w, http.StatusCreated)
	if up.Name != "photo.png" || up.ContentType != "image/png" || up.Size != int64(len(testutil.PNG)) || up.URL != fmt.Sprintf("/files/%d", up.ID) {
		t.Errorf("uploaded file = %+v", up)
	}
	if w.Header().Get("Location") != up.URL {
		t.Errorf("Location = %q, want %q", w.Header().Get("Location"), up.URL)
	}

	// An image is shown in the page; nothing it carries can run.
	w = f.get(f.alice, up.URL)
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), testutil.PNG) {
		t.Fatalf("download: status = %d, %d bytes", w.Code, w.Body.Len())
	}
	h := w.Header()
	if h.Get("Content-Type") != "image/png" || h.Get("Content-Disposition") != `inline; filename=photo.png` ||
		h.Get("Content-Security-Policy") != "default-src 'none'; sandbox" || h.Get("X-Content-Type-Options") != "nosniff" ||
		h.Get("Cache-Control") != "private, no-cache" || h.Get("ETag") == "" {
		t.Errorf("headers of the download: %v", h)
	}

	// The browser asks again with the ETag: the file does not travel twice.
	if w := f.get(f.alice, up.URL, "If-None-Match: "+h.Get("ETag")); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Errorf("with If-None-Match: status = %d, %d bytes", w.Code, w.Body.Len())
	}
	// A part of the file, as a video player or a download that continues asks for it.
	if w := f.get(f.alice, up.URL, "Range: bytes=1-3"); w.Code != http.StatusPartialContent || w.Body.String() != "PNG" {
		t.Errorf("with Range: status = %d, body = %q", w.Code, w.Body)
	}
	// ?download=1 downloads the image instead of showing it.
	if got := f.get(f.alice, up.URL+"?download=1").Header().Get("Content-Disposition"); got != `attachment; filename=photo.png` {
		t.Errorf("with ?download=1: Content-Disposition = %q", got)
	}

	// A document is always downloaded.
	pdf := f.upload(t, f.alice, "Informe año 2026.pdf", testutil.PDF)
	if pdf.ContentType != "application/pdf" {
		t.Errorf("content type = %q, want application/pdf", pdf.ContentType)
	}
	got := f.get(f.alice, pdf.URL).Header().Get("Content-Disposition")
	if !strings.HasPrefix(got, "attachment; filename*=utf-8''Informe%20a%C3%B1o%202026.pdf") {
		t.Errorf("Content-Disposition of a name with accents = %q", got)
	}

	// Only its owner and the administrators: someone else's answers 404, like a note.
	if w := f.get(f.bob, up.URL); w.Code != http.StatusNotFound {
		t.Errorf("someone else's file: status = %d, want 404", w.Code)
	}
	if w := f.get(f.admin, up.URL); w.Code != http.StatusOK {
		t.Errorf("an administrator: status = %d, want 200", w.Code)
	}
	if w := f.get(nil, up.URL); w.Code != http.StatusUnauthorized || w.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("without a session: status = %d, Cache-Control = %q", w.Code, w.Header().Get("Cache-Control"))
	}
	testutil.Decode[testutil.APIError](t, f.get(f.alice, "/files/999999"), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Upload(t, f.api, nil, "/files", "photo.png", testutil.PNG), http.StatusUnauthorized)
}

func TestUploadValidation(t *testing.T) {
	f := setup(t)

	for name, tt := range map[string]struct {
		file    string
		content []byte
		want    string
	}{
		// The type is the one of the bytes: a page renamed to .jpg is still a page.
		"a page with the name of an image": {"photo.jpg", []byte("<html><script>alert(1)</script></html>"), "not allowed"},
		"a program":                        {"setup.exe", append([]byte("MZ"), make([]byte, 100)...), "not allowed"},
		"a ZIP that is not a document":     {"backup.zip", append([]byte("PK\x03\x04"), make([]byte, 100)...), "not allowed"},
		"an empty file":                    {"empty.png", nil, "required"},
		"more than 10 MB":                  {"big.png", append(bytes.Clone(testutil.PNG), make([]byte, 10<<20)...), "10 MB"},
	} {
		w := testutil.Upload(t, f.api, f.alice, "/files", tt.file, tt.content)
		if resp := testutil.Decode[testutil.APIError](t, w, http.StatusUnprocessableEntity); !strings.Contains(resp.Error.Fields["file"], tt.want) {
			t.Errorf("%s: fields = %v, want %q on file", name, resp.Error.Fields, tt.want)
		}
	}

	// The documents of Office are a ZIP: they are told apart by their extension too. A CSV is text.
	docx := f.upload(t, f.alice, "contract.DOCX", append([]byte("PK\x03\x04"), make([]byte, 100)...))
	if !strings.HasSuffix(docx.ContentType, "wordprocessingml.document") {
		t.Errorf("content type of a .docx = %q", docx.ContentType)
	}
	if csv := f.upload(t, f.alice, "data.csv", []byte("name,price\ncoffee,35\n")); csv.ContentType != "text/csv; charset=utf-8" {
		t.Errorf("content type of a .csv = %q", csv.ContentType)
	}

	// An SVG can carry scripts. Its bytes are text, so it is kept as plain text and only downloaded:
	// the browser never draws it, nor runs what it carries.
	svg := f.upload(t, f.alice, "logo.svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`))
	if h := f.get(f.alice, svg.URL).Header(); svg.ContentType != "text/plain; charset=utf-8" || !strings.HasPrefix(h.Get("Content-Disposition"), "attachment") {
		t.Errorf("an SVG: content type = %q, Content-Disposition = %q", svg.ContentType, h.Get("Content-Disposition"))
	}

	// The name is only a name: the path some browsers send, or one made to leave the folder, is cut.
	if up := f.upload(t, f.alice, `..\..\secret/evil.png`, testutil.PNG); up.Name != "evil.png" {
		t.Errorf("name = %q, want evil.png", up.Name)
	}

	// Not a form, and a form without the file.
	w := testutil.Do(t, f.api, f.alice, http.MethodPost, "/files", `{"file":"photo.png"}`)
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusBadRequest); resp.Error.Code != "malformed_upload" {
		t.Errorf("JSON body: code = %q, want malformed_upload", resp.Error.Code)
	}
	r := httptest.NewRequest(http.MethodPost, "/files", strings.NewReader("--x\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nhello\r\n--x--\r\n"))
	r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
	r.AddCookie(f.alice)
	rec := httptest.NewRecorder()
	f.api.ServeHTTP(rec, r)
	if resp := testutil.Decode[testutil.APIError](t, rec, http.StatusUnprocessableEntity); resp.Error.Fields["file"] == "" {
		t.Errorf("a form without the file: %+v", resp.Error)
	}

	// Nothing rejected reached the store.
	if keys := f.store.Keys(); len(keys) != 4 {
		t.Errorf("%d objects in the store, want the 4 accepted", len(keys))
	}
}

func TestDelete(t *testing.T) {
	f := setup(t)
	up := f.upload(t, f.alice, "photo.png", testutil.PNG)

	// Someone else's, and one that does not exist.
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.bob, http.MethodDelete, up.URL, ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.alice, http.MethodDelete, "/files/999999", ""), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, nil, http.MethodDelete, up.URL, ""), http.StatusUnauthorized)

	if w := testutil.Do(t, f.api, f.alice, http.MethodDelete, up.URL, ""); w.Code != http.StatusNoContent {
		t.Fatalf("delete: status = %d; body: %s", w.Code, w.Body)
	}
	// It is no longer served, even before the cleanup deletes its bytes.
	testutil.Decode[testutil.APIError](t, f.get(f.alice, up.URL), http.StatusNotFound)
	testutil.Decode[testutil.APIError](t, testutil.Do(t, f.api, f.alice, http.MethodDelete, up.URL, ""), http.StatusNotFound)

	// One that a resource uses is taken off the resource, not deleted from here.
	attached := f.upload(t, f.alice, "photo.png", testutil.PNG)
	if err := files.Attach(t.Context(), store.New(f.pool), f.aliceID, attached.ID); err != nil {
		t.Fatal(err)
	}
	w := testutil.Do(t, f.api, f.alice, http.MethodDelete, attached.URL, "")
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "file_in_use" {
		t.Errorf("code = %q, want file_in_use", resp.Error.Code)
	}
}

func TestAttachAndRelease(t *testing.T) {
	f := setup(t)
	q := store.New(f.pool)
	ctx := t.Context()
	bob, _ := testutil.Login(t, f.pool, "carol@example.com", auth.RoleUser)

	a, b := f.upload(t, f.alice, "a.png", testutil.PNG), f.upload(t, f.alice, "b.png", testutil.PNG)
	// Someone else's file, one that does not exist, or a list with one of them: nothing is taken.
	for _, ids := range [][]int64{{a.ID}, {999999}} {
		if err := files.Attach(ctx, q, bob.ID, ids...); !errors.Is(err, files.ErrUnknownFile) {
			t.Errorf("Attach(%v) by someone else = %v, want ErrUnknownFile", ids, err)
		}
	}
	if err := files.Attach(ctx, q, f.aliceID, a.ID, b.ID, a.ID); err != nil { // a repeated id counts once
		t.Fatal(err)
	}
	// A file is taken only once.
	if err := files.Attach(ctx, q, f.aliceID, a.ID); !errors.Is(err, files.ErrUnknownFile) {
		t.Errorf("Attach of a file in use = %v, want ErrUnknownFile", err)
	}
	if err := files.Attach(ctx, q, f.aliceID); err != nil {
		t.Errorf("Attach of nothing = %v", err)
	}

	if err := files.Release(ctx, q, a.ID); err != nil {
		t.Fatal(err)
	}
	testutil.Decode[testutil.APIError](t, f.get(f.alice, a.URL), http.StatusNotFound)
	if w := f.get(f.alice, b.URL); w.Code != http.StatusOK {
		t.Errorf("the file that was not released: status = %d", w.Code)
	}
}

func TestQuota(t *testing.T) {
	f := setup(t)
	// Alice already takes all of her 1 GB but 100 bytes.
	_, err := f.pool.Exec(t.Context(), `INSERT INTO files (user_id, key, name, content_type, size, uploaded_at, attached_at)
		VALUES ($1, 'big', 'big.bin', 'application/pdf', $2, now(), now())`, f.aliceID, int64(1<<30-100))
	if err != nil {
		t.Fatal(err)
	}

	w := testutil.Upload(t, f.api, f.alice, "/files", "report.pdf", append(bytes.Clone(testutil.PDF), make([]byte, 100)...))
	if resp := testutil.Decode[testutil.APIError](t, w, http.StatusConflict); resp.Error.Code != "storage_quota_exceeded" {
		t.Errorf("code = %q, want storage_quota_exceeded", resp.Error.Code)
	}
	if len(f.store.Keys()) != 0 {
		t.Error("a file over the quota reached the store")
	}
	// What fits is accepted, and each user has their own space.
	f.upload(t, f.alice, "small.pdf", testutil.PDF)
	f.upload(t, f.bob, "report.pdf", append(bytes.Clone(testutil.PDF), make([]byte, 100)...))
}

func TestUploadRateLimit(t *testing.T) {
	f := setup(t)
	// 30 in a row per user, then 1 every 10 seconds.
	for i := range 30 {
		if w := testutil.Upload(t, f.api, f.alice, "/files", "a.png", testutil.PNG); w.Code != http.StatusCreated {
			t.Fatalf("upload %d: status = %d", i+1, w.Code)
		}
	}
	w := testutil.Upload(t, f.api, f.alice, "/files", "a.png", testutil.PNG)
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "10" {
		t.Errorf("upload 31: status = %d, Retry-After = %q; want 429 and 10", w.Code, w.Header().Get("Retry-After"))
	}
	// The limit is per user.
	f.upload(t, f.bob, "a.png", testutil.PNG)
}

// failingStore fails the uploads, like a bucket that does not answer.
type failingStore struct{ *storage.Memory }

func (failingStore) Put(context.Context, string, io.ReadSeeker, int64, string) error {
	return errors.New("the bucket does not answer")
}

// streamStore returns the objects as S3 does: a body that arrives, without jumping inside it.
type streamStore struct{ *storage.Memory }

func (s streamStore) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	body, err := s.Memory.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	return struct {
		io.Reader
		io.Closer
	}{body, body}, nil
}

// With S3 the file is sent whole as it arrives from the bucket, also when a part of it is asked for.
func TestDownloadFromAStoreThatStreams(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithStorage(streamStore{storage.NewMemory()}))
	_, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)
	f := fixture{api: api}
	up := testutil.Decode[file](t, testutil.Upload(t, api, cookie, "/files", "photo.png", testutil.PNG), http.StatusCreated)

	for _, headers := range [][]string{nil, {"Range: bytes=1-3"}} {
		w := f.get(cookie, up.URL, headers...)
		if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), testutil.PNG) || w.Header().Get("Content-Length") != fmt.Sprint(len(testutil.PNG)) {
			t.Errorf("with %v: status = %d, %d bytes, Content-Length = %q", headers, w.Code, w.Body.Len(), w.Header().Get("Content-Length"))
		}
	}
	if w := f.get(cookie, up.URL, "If-None-Match: "+f.get(cookie, up.URL).Header().Get("ETag")); w.Code != http.StatusNotModified {
		t.Errorf("with If-None-Match: status = %d, want 304", w.Code)
	}
}

func TestUploadWhenTheStoreFails(t *testing.T) {
	pool := testutil.NewDB(t)
	api := testutil.NewAPI(pool, testutil.WithStorage(failingStore{storage.NewMemory()}))
	alice, cookie := testutil.Login(t, pool, "alice@example.com", auth.RoleUser)

	testutil.Decode[testutil.APIError](t, testutil.Upload(t, api, cookie, "/files", "a.png", testutil.PNG), http.StatusInternalServerError)

	// The row is marked for the cleanup, and does not count for the quota.
	var pending int
	if err := pool.QueryRow(t.Context(), "SELECT count(*) FROM files WHERE deleted_at IS NULL").Scan(&pending); err != nil || pending != 0 {
		t.Errorf("%d files left as if the upload were in progress (%v)", pending, err)
	}
	used, err := store.New(pool).SumUserFileSizes(t.Context(), &alice.ID)
	if err != nil || used != 0 {
		t.Errorf("space used = %d (%v), want 0", used, err)
	}
}

func TestPurge(t *testing.T) {
	f := setup(t)
	q := store.New(f.pool)
	ctx := t.Context()
	age := func(id int64) {
		t.Helper()
		if _, err := f.pool.Exec(ctx, "UPDATE files SET created_at = now() - interval '25 hours' WHERE id = $1", id); err != nil {
			t.Fatal(err)
		}
	}

	inUse := f.upload(t, f.alice, "in-use.png", testutil.PNG)
	if err := files.Attach(ctx, q, f.aliceID, inUse.ID); err != nil {
		t.Fatal(err)
	}
	age(inUse.ID) // a file in use stays, however old

	fresh := f.upload(t, f.alice, "just-uploaded.png", testutil.PNG) // the form may still be open

	forgotten := f.upload(t, f.alice, "forgotten.png", testutil.PNG) // uploaded yesterday, never used
	age(forgotten.ID)

	released := f.upload(t, f.alice, "released.png", testutil.PNG)
	if err := files.Release(ctx, q, released.ID); err != nil {
		t.Fatal(err)
	}

	// An upload cut before its bytes arrived: the row is there, without uploaded_at.
	cut, err := q.CreateFile(ctx, store.CreateFileParams{UserID: &f.aliceID, Key: "2026/11/cut", Name: "cut.png", ContentType: "image/png", Size: 10})
	if err != nil {
		t.Fatal(err)
	}
	age(cut.ID)

	// The files of a deleted account, in use or not.
	carol, carolCookie := testutil.Login(t, f.pool, "carol@example.com", auth.RoleUser)
	orphan := f.upload(t, carolCookie, "orphan.png", testutil.PNG)
	if err := files.Attach(ctx, q, carol.ID, orphan.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.pool.Exec(ctx, "DELETE FROM users WHERE id = $1", carol.ID); err != nil {
		t.Fatal(err)
	}

	deleted, err := files.Purge(ctx, q, f.store)
	if err != nil || deleted != 4 {
		t.Fatalf("Purge = %d, %v; want 4 files", deleted, err)
	}
	if keys := f.store.Keys(); len(keys) != 2 {
		t.Errorf("%d objects left in the store, want 2", len(keys))
	}
	var left []int64
	rows, _ := f.pool.Query(ctx, "SELECT id FROM files ORDER BY id")
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		left = append(left, id)
	}
	if len(left) != 2 || left[0] != inUse.ID || left[1] != fresh.ID {
		t.Errorf("files left = %v, want %d and %d", left, inUse.ID, fresh.ID)
	}
	// Nothing else to delete.
	if deleted, err := files.Purge(ctx, q, f.store); err != nil || deleted != 0 {
		t.Errorf("a second Purge = %d, %v", deleted, err)
	}
}

// A file over a slow connection takes longer than the server's limits, which are made for JSON
// requests: the upload has its own. Here the server cuts at half a second, and the upload takes more
// than a second.
func TestSlowUpload(t *testing.T) {
	f := setup(t)
	srv := httptest.NewUnstartedServer(f.api)
	srv.Config.ReadTimeout, srv.Config.WriteTimeout = 500*time.Millisecond, 500*time.Millisecond
	srv.Start()
	defer srv.Close()

	body, bodyWriter := io.Pipe()
	form := multipart.NewWriter(bodyWriter)
	go func() {
		part, _ := form.CreateFormFile("file", "slow.png")
		_, _ = part.Write(testutil.PNG)
		for range 4 {
			time.Sleep(300 * time.Millisecond)
			_, _ = part.Write(make([]byte, 1024))
		}
		_ = form.Close()
		_ = bodyWriter.Close()
	}()

	r, err := http.NewRequestWithContext(t.Context(), http.MethodPost, srv.URL+"/files", body)
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set("Content-Type", form.FormDataContentType())
	r.AddCookie(f.alice)
	resp, err := srv.Client().Do(r)
	if err != nil {
		t.Fatalf("the slow upload was cut: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Errorf("status = %d, want 201", resp.StatusCode)
	}
}
