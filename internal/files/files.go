// Package files is the uploaded files: it receives them, records whose each one is, serves them and
// deletes the ones nobody needs. The bytes are in a store (internal/storage: the local disk or S3)
// and the rest in the files table. See docs/files.md.
//
// A resource uses a file in three steps: Receive reads it from the request, Attach marks it as in
// use inside the transaction that links it to the resource, and Release marks it to be deleted when
// the resource stops using it. Serve sends it, after the resource checked who can see it. The notes
// example does all of it (internal/notes/attachments.go).
package files

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/NoeOnDev/go-api-base/internal/auth"
	"github.com/NoeOnDev/go-api-base/internal/httpx"
	"github.com/NoeOnDev/go-api-base/internal/i18n"
	"github.com/NoeOnDev/go-api-base/internal/storage"
	"github.com/NoeOnDev/go-api-base/internal/store"
)

// Types of file a Rule can allow. They are the types found in the bytes (see detect), without
// parameters.
var (
	Images    = []string{"image/jpeg", "image/png", "image/gif", "image/webp"}
	Documents = []string{"application/pdf", typeDOCX, typeXLSX, typePPTX, "text/csv", "text/plain"}
)

const (
	typeDOCX = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	typeXLSX = "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	typePPTX = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
)

// Rule is what an upload accepts.
type Rule struct {
	// MaxBytes is the largest file.
	MaxBytes int64
	// Types are the types allowed (Images, Documents, or a list of the project).
	Types []string
}

// DefaultRule is the rule of POST /files: images and documents up to 10 MB.
var DefaultRule = Rule{MaxBytes: 10 << 20, Types: slices.Concat(Images, Documents)}

const (
	// formField is the field of the form that carries the file.
	formField = "file"
	// formOverhead is what the rest of the form can take, besides the file.
	formOverhead = 64 << 10
	// transferTimeout is how long an upload or a download can take. The limits of the server
	// (ReadTimeout, WriteTimeout) are for JSON requests: a file over a slow connection needs more.
	transferTimeout = 5 * time.Minute
	// unattachedTTL is how long a file is kept without a resource taking it.
	unattachedTTL = 24 * time.Hour
	// uploadGrace is how long the cleanup leaves alone a marked file whose upload did not finish: its
	// bytes can still be on their way to the store. Well over transferTimeout.
	uploadGrace = time.Hour
	// purgeBatch is how many files the cleanup deletes per query.
	purgeBatch = 200
	// maxNameLength is the longest name kept, in characters.
	maxNameLength = 200
)

// File is a file as the API returns it. A resource puts it in its own response.
type File struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	ContentType string `json:"content_type"`
	Size        int64  `json:"size"`
	// URL is the path of GET /files/{id}, which only its owner and the administrators can open. A
	// resource that serves its files to others (with Service.Serve) returns its own route instead.
	URL       string    `json:"url"`
	CreatedAt time.Time `json:"created_at"`
}

// ToResponse turns a row into what the API returns.
func ToResponse(f store.File) File {
	return File{
		ID: f.ID, Name: f.Name, ContentType: f.ContentType, Size: f.Size,
		URL: "/files/" + strconv.FormatInt(f.ID, 10), CreatedAt: f.CreatedAt,
	}
}

// ErrUnknownFile is what Attach returns when one of the files cannot be used: it does not exist, it
// is someone else's, it is already in use or it was deleted. The resource answers it as a validation
// error on its field.
var ErrUnknownFile = errors.New("files: unknown file")

var (
	errNotFound        = httpx.NotFound("file_not_found")
	errInUse           = httpx.Conflict("file_in_use", "")
	errMalformedUpload = httpx.BadRequest("malformed_upload")
	errQuotaExceeded   = httpx.Conflict("storage_quota_exceeded", "")
)

// Options are the limits of the uploads.
type Options struct {
	// UserQuota is how many bytes the files of one user can add up to. 0, no limit.
	UserQuota int64
}

// Service receives and serves the files.
type Service struct {
	db    *pgxpool.Pool
	q     *store.Queries
	store storage.Store
	opts  Options
}

// NewService creates the service over a store (see internal/storage).
func NewService(pool *pgxpool.Pool, q *store.Queries, st storage.Store, opts Options) *Service {
	return &Service{db: pool, q: q, store: st, opts: opts}
}

// Receive reads the file of a multipart/form-data request (the "file" field, as a browser's
// FormData sends it), checks it against rule and stores it as a file of the session's user. The
// file is not in use yet: the resource calls Attach in its transaction, and one that nothing takes
// in a day is deleted.
//
// The type is the one found in the bytes, never the one the request says: a page renamed to
// photo.jpg is still a page, and is rejected.
func (s *Service) Receive(w http.ResponseWriter, r *http.Request, rule Rule) (store.File, error) {
	ctx := r.Context()
	user := auth.MustUser(ctx)
	extendDeadlines(w)

	r.Body = http.MaxBytesReader(w, r.Body, rule.MaxBytes+formOverhead)
	form, err := r.MultipartReader()
	if err != nil {
		return store.File{}, errMalformedUpload
	}
	part, err := filePart(form)
	if err != nil {
		return store.File{}, err
	}

	// To a temporary file first: the size and the type are known before anything reaches the store,
	// and the store gets a body it can read twice.
	tmp, err := os.CreateTemp("", "upload-*")
	if err != nil {
		return store.File{}, fmt.Errorf("create the temporary file of the upload: %w", err)
	}
	defer func() {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
	}()
	disk := &writeErrors{w: tmp}
	size, err := io.Copy(disk, io.LimitReader(part, rule.MaxBytes+1))
	tooLarge := httpx.Validation(httpx.Fields{formField: i18n.M("validation.file_too_large", "max", humanSize(rule.MaxBytes))})
	var maxBytesErr *http.MaxBytesError
	switch {
	case disk.err != nil:
		// The server's own failure (the disk of the temporary files is full): a 500 that is logged,
		// not a 400 that blames the file.
		return store.File{}, fmt.Errorf("write the upload to a temporary file: %w", disk.err)
	case errors.As(err, &maxBytesErr), size > rule.MaxBytes:
		return store.File{}, tooLarge
	case err != nil:
		return store.File{}, errMalformedUpload // the connection was cut, or the form is broken
	case size == 0:
		return store.File{}, httpx.Validation(httpx.Fields{formField: i18n.M("validation.required")})
	}

	name := cleanName(part.FileName())
	head := make([]byte, 512)
	n, err := tmp.ReadAt(head, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return store.File{}, fmt.Errorf("read the upload: %w", err)
	}
	contentType := detect(head[:n], name)
	if !rule.allows(contentType) {
		return store.File{}, httpx.Validation(httpx.Fields{formField: i18n.M("validation.file_type")})
	}

	// The row first, without uploaded_at: if the process dies in the middle, the cleanup finds the
	// row and deletes what reached the store. The user's row is locked to add up the quota: two
	// uploads sent at once wait for each other here.
	key := newKey()
	var file store.File
	err = pgx.BeginFunc(ctx, s.db, func(tx pgx.Tx) error {
		q := s.q.WithTx(tx)
		if err := q.LockUser(ctx, user.ID); err != nil {
			return fmt.Errorf("lock user %d: %w", user.ID, err)
		}
		if s.opts.UserQuota > 0 {
			used, err := q.SumUserFileSizes(ctx, &user.ID)
			if err != nil {
				return fmt.Errorf("add up the files of user %d: %w", user.ID, err)
			}
			if used+size > s.opts.UserQuota {
				return errQuotaExceeded
			}
		}
		var err error
		file, err = q.CreateFile(ctx, store.CreateFileParams{
			UserID: &user.ID, Key: key, Name: name, ContentType: contentType, Size: size,
		})
		if err != nil {
			return fmt.Errorf("create file: %w", err)
		}
		return nil
	})
	if err != nil {
		return store.File{}, err
	}

	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		return store.File{}, fmt.Errorf("rewind the upload: %w", err)
	}
	// The store has its own limit: a bucket that stops answering must not hold the request, and its
	// temporary file, for as long as the connection lasts.
	putCtx, cancel := context.WithTimeout(ctx, transferTimeout)
	defer cancel()
	if err := s.store.Put(putCtx, key, tmp, size, contentType); err != nil {
		s.releaseFailed(ctx, file.ID)
		return store.File{}, fmt.Errorf("store file %d: %w", file.ID, err)
	}
	uploaded, err := s.q.MarkFileUploaded(ctx, file.ID)
	if err != nil {
		// The client left when the bytes had arrived, or the file was marked meanwhile (its account
		// was deleted): nobody got its id, so it is not left counting for the quota for a day.
		s.releaseFailed(ctx, file.ID)
		return store.File{}, fmt.Errorf("record the upload of file %d: %w", file.ID, err)
	}
	return uploaded, nil
}

// releaseFailed marks a file whose upload failed: it stops counting for the quota now, and the
// cleanup deletes the row and whatever reached the store. It does not depend on the request, which
// may be the one that was cut.
func (s *Service) releaseFailed(ctx context.Context, id int64) {
	if err := s.q.ReleaseFiles(context.WithoutCancel(ctx), []int64{id}); err != nil {
		httpx.LoggerFrom(ctx).Error("could not release a file whose upload failed", "file_id", id, "error", err)
	}
}

// writeErrors remembers the error of a write, to tell a disk that fails from a body that does.
type writeErrors struct {
	w   io.Writer
	err error
}

func (w *writeErrors) Write(p []byte) (int, error) {
	n, err := w.w.Write(p)
	if err != nil {
		w.err = err
	}
	return n, err
}

// Attach marks the user's files as in use by a resource. Call it inside the transaction that links
// them (q is its queries, h.q.WithTx(tx)), so a file is never in use without its link. A file is
// taken only once: two resources do not share it, because each one releases it when it is done.
// It returns ErrUnknownFile if one of them cannot be taken.
//
// The file is still its user's: when their account is deleted, it is deleted with it. That is right
// for the data of each user (their avatar, the attachments of their notes). For a file of a resource
// that outlives who uploaded it, AttachShared.
func Attach(ctx context.Context, q *store.Queries, userID int64, ids ...int64) error {
	return attach(ctx, q, userID, false, ids)
}

// AttachShared is Attach for a file that belongs to its resource and not to who uploaded it: the
// image of a product, the logo of a company. It stays when that person's account is deleted, until
// the resource releases it (Release). Only who uploaded it can attach it, like with Attach.
func AttachShared(ctx context.Context, q *store.Queries, userID int64, ids ...int64) error {
	return attach(ctx, q, userID, true, ids)
}

func attach(ctx context.Context, q *store.Queries, userID int64, shared bool, ids []int64) error {
	ids = slices.Compact(slices.Sorted(slices.Values(ids)))
	if len(ids) == 0 {
		return nil
	}
	rows, err := q.AttachFiles(ctx, store.AttachFilesParams{Ids: ids, UserID: &userID, Shared: shared})
	if err != nil {
		return fmt.Errorf("attach files: %w", err)
	}
	if rows != int64(len(ids)) {
		return ErrUnknownFile
	}
	return nil
}

// Release marks the files to be deleted: they stop being served now, and the hourly cleanup deletes
// their bytes and their rows. Call it in the transaction that deletes the resource, or that takes
// the file off it.
func Release(ctx context.Context, q *store.Queries, ids ...int64) error {
	if len(ids) == 0 {
		return nil
	}
	if err := q.ReleaseFiles(ctx, ids); err != nil {
		return fmt.Errorf("release files: %w", err)
	}
	return nil
}

// Serve sends the file. It does not check who asks: the caller did it (GET /files/{id} checks that
// it is the owner; a resource, its own rule). A file that does not exist, or was released, answers
// 404 file_not_found.
//
// The file goes with headers that keep it from running in the browser with the session of whoever
// opens it: only the images are shown in the page, everything else is downloaded, and nothing can
// run scripts. With ?download=1, the images are downloaded too.
func (s *Service) Serve(w http.ResponseWriter, r *http.Request, id int64) error {
	file, err := s.q.GetFile(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		return errNotFound
	}
	if err != nil {
		return fmt.Errorf("get file %d: %w", id, err)
	}
	return s.serve(w, r, file)
}

func (s *Service) serve(w http.ResponseWriter, r *http.Request, file store.File) error {
	// The key is new for each upload and its bytes never change, so it identifies the content.
	etag := strconv.Quote(path.Base(file.Key))
	h := w.Header()
	// The browser keeps it but asks every time: the permission is checked again, and the file only
	// travels if it changed (never: the answer is 304).
	h.Set("Cache-Control", "private, no-cache")
	h.Set("ETag", etag)
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return nil
	}

	body, err := s.store.Open(r.Context(), file.Key)
	if err != nil {
		// The answer is an error in JSON, not the file: it does not go with the file's headers.
		h.Del("ETag")
		h.Set("Cache-Control", "no-store")
		if errors.Is(err, storage.ErrNotFound) {
			httpx.LoggerFrom(r.Context()).Error("a file has its row but not its bytes", "file_id", file.ID, "key", file.Key)
			return errNotFound
		}
		return fmt.Errorf("open file %d: %w", file.ID, err)
	}
	defer body.Close()

	disposition := "attachment"
	if base, _, _ := mime.ParseMediaType(file.ContentType); slices.Contains(Images, base) && r.URL.Query().Get("download") == "" {
		disposition = "inline"
	}
	h.Set("Content-Type", file.ContentType)
	h.Set("Content-Disposition", contentDisposition(disposition, file.Name))
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")

	extendDeadlines(w)
	// A store on disk can jump inside the file: ServeContent answers a part of it (Range), which a
	// video or a download that continues asks for. S3 sends it whole.
	if seeker, ok := body.(io.ReadSeeker); ok {
		http.ServeContent(w, r, "", file.CreatedAt, seeker)
		return nil
	}
	h.Set("Content-Length", strconv.FormatInt(file.Size, 10))
	if r.Method == http.MethodHead {
		return nil
	}
	if _, err := io.Copy(w, body); err != nil {
		// The headers are sent: it cannot be answered as an error anymore.
		httpx.LoggerFrom(r.Context()).Warn("a download was cut", "file_id", file.ID, "error", err)
	}
	return nil
}

// Purge deletes the files nobody needs, the bytes first and then the row: the ones released, those
// of a deleted account (unless they are shared), and the ones nothing took in a day (an upload that
// was cut, a form that was never sent). The hourly cleanup calls it (internal/jobs). It returns how
// many it deleted.
//
// It marks them first, in one statement, and then deletes the marked ones: a file a resource takes
// at that moment is either taken or marked, never deleted after being taken.
func Purge(ctx context.Context, q *store.Queries, st storage.Store) (int64, error) {
	now := time.Now()
	if _, err := q.MarkUnusedFiles(ctx, now.Add(-unattachedTTL)); err != nil {
		return 0, fmt.Errorf("mark the files nobody uses: %w", err)
	}
	var deleted int64
	for {
		rows, err := q.ListPurgeableFiles(ctx, store.ListPurgeableFilesParams{
			UploadStartedBefore: now.Add(-uploadGrace), Limit: purgeBatch,
		})
		if err != nil {
			return deleted, fmt.Errorf("list the files to delete: %w", err)
		}
		var errs []error
		for _, f := range rows {
			if err := st.Delete(ctx, f.Key); err != nil {
				errs = append(errs, err)
				continue
			}
			if err := q.DeleteFile(ctx, f.ID); err != nil {
				errs = append(errs, fmt.Errorf("delete file %d: %w", f.ID, err))
				continue
			}
			deleted++
		}
		// With an error the same rows would come back: the next cleanup tries them again.
		if len(errs) > 0 || len(rows) < purgeBatch {
			return deleted, errors.Join(errs...)
		}
	}
}

// filePart returns the part of the form with the file, skipping the fields before it.
func filePart(form *multipart.Reader) (*multipart.Part, error) {
	for {
		part, err := form.NextPart()
		if errors.Is(err, io.EOF) {
			return nil, httpx.Validation(httpx.Fields{formField: i18n.M("validation.required")})
		}
		if err != nil {
			return nil, errMalformedUpload
		}
		if part.FormName() == formField && part.FileName() != "" {
			return part, nil
		}
	}
}

func (r Rule) allows(contentType string) bool {
	base, _, err := mime.ParseMediaType(contentType)
	return err == nil && slices.Contains(r.Types, base)
}

// detect returns the type of a file from its first bytes, as a browser would guess it. Two cases
// need the extension too, because their bytes do not tell them apart: the documents of Office, which
// are a ZIP, and a CSV, which is plain text. The extension alone is never enough.
func detect(head []byte, name string) string {
	contentType := http.DetectContentType(head)
	base, _, _ := mime.ParseMediaType(contentType)
	ext := strings.ToLower(path.Ext(name))
	switch {
	case base == "application/zip":
		switch ext {
		case ".docx":
			return typeDOCX
		case ".xlsx":
			return typeXLSX
		case ".pptx":
			return typePPTX
		}
	case base == "text/plain" && ext == ".csv":
		return "text/csv" + strings.TrimPrefix(contentType, "text/plain")
	}
	return contentType
}

// cleanName leaves the name the user sent as a plain file name: without the path some browsers
// send, without control characters, and not too long. It is only shown and offered for the
// download: the bytes are stored under a random key.
func cleanName(name string) string {
	name = name[strings.LastIndexAny(name, `/\`)+1:]
	if !httpx.ValidText(name) {
		name = ""
	}
	name = strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, name))
	if runes := []rune(name); len(runes) > maxNameLength {
		// The end is kept: it has the extension.
		name = string(runes[len(runes)-maxNameLength:])
	}
	if name == "" || name == "." || name == ".." {
		return "file"
	}
	return name
}

// newKey makes the key of a new file in the store: the month, to find one's way around the bucket,
// and a random text.
func newKey() string {
	return time.Now().UTC().Format("2006/01") + "/" + strings.ToLower(rand.Text())
}

// contentDisposition writes the header with the name of the file, encoded if it has characters
// outside ASCII.
func contentDisposition(disposition, name string) string {
	if v := mime.FormatMediaType(disposition, map[string]string{"filename": name}); v != "" {
		return v
	}
	return disposition
}

// humanSize writes a size for a message: "10 MB", "512 KB".
func humanSize(bytes int64) string {
	if bytes >= 1<<20 {
		return strconv.FormatFloat(float64(bytes)/(1<<20), 'f', -1, 64) + " MB"
	}
	return strconv.FormatInt(bytes>>10, 10) + " KB"
}

// extendDeadlines gives the request the time of a file transfer instead of the server's limits.
func extendDeadlines(w http.ResponseWriter) {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(transferTimeout)
	_ = rc.SetReadDeadline(deadline)
	_ = rc.SetWriteDeadline(deadline)
}
