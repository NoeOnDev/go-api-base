# Files

The API receives files (an avatar, an attachment, a document), keeps them on the local disk or in S3, and serves them to whoever is allowed to see them. It is the equivalent of Laravel's `Storage` plus a table that says whose each file is.

Two packages do it:

| Package | What it knows |
|---|---|
| `internal/storage` | Bytes under a key. Three stores: `Local` (a directory), `S3` (Amazon S3 or a compatible service, with the official SDK) and `Memory` (the tests) |
| `internal/files` | The `files` table (owner, name, type, size), the upload, the download, and the cleanup of the files nobody needs |

A resource never talks to the store: it uses `internal/files`, and stores the `id` of the file.

## Configuration

| Variable | Default | What for |
|---|---|---|
| `STORAGE_DRIVER` | `local` | `local` (a directory of the server) or `s3` |
| `STORAGE_DIR` | `storage` | The directory of the local driver. It is created on startup |
| `S3_BUCKET`, `S3_REGION` | | Required with `s3`. With Cloudflare R2, the region is `auto` |
| `S3_ENDPOINT` | | The address of a compatible service (R2, MinIO, DigitalOcean Spaces). Empty, Amazon S3 |
| `S3_PATH_STYLE` | `false` | `true` puts the bucket in the path (`https://host/bucket/key`), as MinIO needs |
| `S3_ACCESS_KEY_ID`, `S3_SECRET_ACCESS_KEY` | | Fixed credentials. Empty, the SDK takes them from the role of the machine (EC2, ECS, EKS) or from the `AWS_*` variables |

**In development, `local`:** nothing to install, and the files are in `storage/` (ignored by git).

**In production, `s3`** unless the API runs on a single server whose disk is backed up. With several instances, each one has its own disk: `local` only works with a volume shared by all of them.

**The bucket is private.** The files reach the browser through the API, which checks the permissions on each request; nothing links to the bucket, so it needs no public access and no CORS rules. The credentials only need `s3:PutObject`, `s3:GetObject` and `s3:DeleteObject` on that bucket. (Without `s3:ListBucket`, Amazon S3 answers "access denied" instead of "not found" for an object that is missing: a file with its row and without its bytes, which should not happen, then answers `500` instead of `404`.)

**On AWS, prefer the role of the machine** to fixed keys: leave `S3_ACCESS_KEY_ID` empty and give the role those three permissions. There is no secret to store or rotate.

A wrong bucket or key does not stop the startup: it shows on the first upload, as a `500` with the cause in the log.

## Endpoints

| Route | What it does |
|---|---|
| `POST /files` | With a session. A `multipart/form-data` body with the file in the `file` field. It answers `201` with the file. Images and documents up to 10 MB |
| `GET /files/{id}` | The file, for its owner or an administrator; someone else's answers `404`. With `?download=1`, an image is downloaded instead of shown |
| `DELETE /files/{id}` | Deletes a file of the user that no resource uses. One in use answers `409 file_in_use`: it is taken off its resource |

A file as the API returns it:

```json
{"id": 12, "name": "ticket.pdf", "content_type": "application/pdf", "size": 48213,
 "url": "/files/12", "created_at": "2026-11-19T15:04:05Z"}
```

The notes example adds its own routes (`internal/notes/attachments.go`):

| Route | What it does |
|---|---|
| `POST /notes/{id}/attachments` | Uploads a file (same body as `POST /files`, up to 5 MB) and adds it to the note |
| `GET /notes/{id}/attachments` | The files of the note: `{"data": [...]}` |
| `DELETE /notes/{id}/attachments/{file_id}` | Takes the file off the note and deletes it |

Errors of an upload:

| Response | When |
|---|---|
| `400 malformed_upload` | The body is not `multipart/form-data`, or the connection was cut |
| `422` on `file` | No file, an empty one, one larger than the route allows, or a type it does not allow |
| `409 storage_quota_exceeded` | The files of the user already take their space (1 GB) |
| `429 too_many_requests` | More than 30 uploads in a row (then one every 10 seconds); see `Retry-After` |

## From the frontend (React)

A file is sent with `FormData`. Do not set `Content-Type`: the browser writes it, with the boundary of the form.

```ts
async function upload(file: File): Promise<UploadedFile> {
  const body = new FormData();
  body.append("file", file);
  const res = await fetch(`${API}/files`, { method: "POST", body, credentials: "include" });
  if (!res.ok) throw await res.json(); // {error: {code, message, fields: {file: "…"}}}
  return res.json();
}
```

To show or download it, use its `url`. The request carries the session cookie, like any other:

```tsx
<img src={`${API}${file.url}`} alt={file.name} />
<a href={`${API}${file.url}?download=1`}>Download</a>
```

With the frontend on another domain, the cookie only travels in an `<img>` if both share the site (`app.example.com` and `api.example.com`); see "From the frontend" in [auth.md](auth.md#from-the-frontend-react). The browser keeps each file and asks again with its `ETag`: the API checks the permission every time, and answers `304` without sending the file again.

## What an upload accepts

Each upload route has a rule: the largest size and the types allowed.

```go
var attachmentRule = files.Rule{MaxBytes: 5 << 20, Types: slices.Concat(files.Images, files.Documents)}
```

| List | Types |
|---|---|
| `files.Images` | JPEG, PNG, GIF, WebP |
| `files.Documents` | PDF, Word (`.docx`), Excel (`.xlsx`), PowerPoint (`.pptx`), CSV, plain text |

**The type is the one found in the bytes**, never the one the browser says, nor the extension: a page renamed to `photo.jpg` is still a page, and is rejected. Two cases need the extension too, because their bytes do not tell them apart: the Office documents, which are a ZIP, and a CSV, which is plain text. A ZIP named `report.docx` is accepted as a Word document; it is only ever downloaded, never opened by the browser.

An SVG is text that can carry scripts. It is not in `files.Images`: if a route accepts plain text, an SVG is kept as plain text and only downloaded.

To accept another type, add what `http.DetectContentType` returns for it to the rule's list (`"video/mp4"`, `"application/zip"`). For a larger file, raise `MaxBytes`; an upload can take up to 5 minutes (`transferTimeout` in `internal/files`).

## Using a file in a resource

Three calls, always the same:

| Call | When | What it does |
|---|---|---|
| `filesService.Receive(w, r, rule)` | In the upload route | Reads the file, checks it against the rule and stores it. It belongs to the session's user, and nothing uses it yet |
| `files.Attach(ctx, q, userID, ids...)` | In the transaction that links it to the resource | Marks it as in use. A file is taken only once |
| `files.AttachShared(ctx, q, userID, ids...)` | The same, for a file that belongs to the resource and not to who uploaded it | Like `Attach`, and the file stays when that person's account is deleted (see "Whose a file is") |
| `files.Release(ctx, q, ids...)` | In the transaction that deletes the resource, or takes the file off it | Marks it to be deleted: it stops being served now, and the cleanup deletes it |

The table of the resource references the file. For one file per row (an avatar, a logo):

```sql
avatar_id bigint REFERENCES files (id) ON DELETE SET NULL
```

For several (the attachments of a note), a table in between, like `note_attachments` (`migrations/00017_create_note_attachments.sql`).

**When the resource already exists**, give it its own upload route, which receives and attaches in one request. It is what the notes do:

```go
func (h *Handler) attach(w http.ResponseWriter, r *http.Request) error {
	id, err := h.ownNote(r) // the note first: the body is not read for someone else's note
	if err != nil {
		return err
	}
	file, err := h.files.Receive(w, r, attachmentRule)
	if err != nil {
		return err
	}
	user := auth.MustUser(r.Context())
	err = pgx.BeginFunc(r.Context(), h.db, func(tx pgx.Tx) error {
		q := h.q.WithTx(tx)
		if err := files.Attach(r.Context(), q, user.ID, file.ID); err != nil {
			return err
		}
		err := q.CreateNoteAttachment(r.Context(), store.CreateNoteAttachmentParams{NoteID: id, FileID: file.ID})
		if _, ok := db.IsForeignKeyViolation(err); ok {
			return errNotFound // the note was deleted while the file was uploading
		}
		return err
	})
	if err != nil {
		_ = files.Release(context.WithoutCancel(r.Context()), h.q, file.ID) // it has no note: it is not kept
		return err
	}
	httpx.Write(w, http.StatusCreated, files.ToResponse(file))
	return nil
}
```

Its route has the limit of uploads per user: `files.ByUser(uploads)`, inside `RequireUser` (see `internal/notes/routes.go`).

**When the resource does not exist yet** (a form that creates it, with a file picked before sending), the frontend uploads to `POST /files`, and sends the `id` it got with the rest of the form. The handler attaches it in the transaction that creates the resource:

```go
err := files.Attach(ctx, q, user.ID, in.AvatarID)
if errors.Is(err, files.ErrUnknownFile) {
	return httpx.Validation(httpx.Fields{"avatar_id": i18n.M("validation.unknown_file")}) // a key of the project
}
```

`ErrUnknownFile` covers a file that does not exist, is someone else's, is already in use or was deleted: a user cannot attach a file they did not upload.

**Replacing a file** (a new avatar) is attaching the new one and releasing the old one, in the same transaction.

**Deleting the resource** releases its files in the same transaction, as `delete` does in `internal/notes/handlers.go`. Without it, the files would stay in use by a resource that no longer exists, and nothing would delete them. That transaction locks the resource first (`LockNote`, a `SELECT … FOR UPDATE`) and reads its files after: a file that is being attached at that moment waits, and then finds the resource gone. Reading the files first, that one would be missed and stay in use forever.

**Whose a file is.** With `Attach`, the file is still its user's: when their account is deleted, the file is deleted too, wherever it is attached. That is right for the data of each user (an avatar, the attachments of their notes), which goes with the account. A file of a resource that outlives who uploaded it (the image of a product, the logo of a company) is taken with `files.AttachShared` instead: it stays until the resource releases it. It still counts for the quota of who uploaded it while their account exists.

**Who can see a file.** `GET /files/{id}` only lets the owner and the administrators in, which is right for a user's own data. A resource whose files others see (the image of a product, the avatar in a list) serves them from its own route, after its own check:

```go
// GET /products/{id}/image, public
func (h *Handler) image(w http.ResponseWriter, r *http.Request) error {
	product, err := h.find(r) // 404 if it does not exist, or if this visitor cannot see it
	if err != nil {
		return err
	}
	return h.files.Serve(w, r, product.ImageID)
}
```

and returns that route, not `url`, in its JSON.

## The life of a file

| State | In the table | What happens |
|---|---|---|
| Uploading | `uploaded_at` is `NULL` | The row exists before the bytes arrive. It is not served |
| Uploaded | `uploaded_at` set, `attached_at` `NULL` | Served to its owner. If nothing takes it in a day, it is deleted |
| In use | `attached_at` set | It stays until its resource releases it |
| Released | `deleted_at` set | No longer served. The cleanup deletes it |

The hourly cleanup (`files.Purge`, from the `cleanup` job; see [jobs.md](jobs.md)) deletes the released files, the ones uploaded more than a day ago that nothing took, and those of a deleted account that are not shared. It marks them first (`deleted_at`), in one statement, and then deletes the marked ones, the bytes first and the row after. A form sent at that same moment either takes its file or gets an error for it (`ErrUnknownFile`): a file is never deleted after a resource took it.

**A row lives longer than its bytes**, at both ends: it is created before the upload and deleted after the bytes. So whatever fails in the middle (the process dies, the bucket does not answer), a row remembers the object, and the cleanup finds it. A marked file whose upload did not finish waits an hour before it is deleted, so its row does not go while its bytes are still arriving. Nothing is left in the bucket that the database does not know about.

On the local disk, a file is written under `.tmp/` in `STORAGE_DIR` and renamed when it is complete. A process that dies in the middle (the server is turned off) leaves that temporary file, which no row knows: the startup deletes the ones older than a day.

**Deleting an account** (`DELETE /auth/me`, or an administrator) does not delete its files in that request: `files.user_id` becomes `NULL`, only the administrators can open them, and the next cleanup deletes them. The shared ones stay (see "Whose a file is").

## Security

- **A file never runs in the browser with the session of whoever opens it.** Files are served from the API's domain, where the session cookie is valid, so an uploaded page would be a stored XSS. Only the images are shown; everything else goes with `Content-Disposition: attachment`. Every file carries `Content-Security-Policy: default-src 'none'; sandbox` and `X-Content-Type-Options: nosniff`.
- **The type comes from the bytes** (see "What an upload accepts").
- **The name is only shown.** The bytes are stored under a random key (`2026/11/…`), never under the name the user sent, which is cleaned of paths and control characters. The local store cannot write outside its directory (`os.Root`).
- **Limits per user,** in PostgreSQL like the other per-user limits, so they hold on every instance: 30 uploads in a row, and 1 GB in total. Both are in `internal/server/routes.go`.
- **The size is checked while reading**: a larger body is cut, not stored and then measured.
- **The bucket is private**, and the API checks the permission on every download, including the ones the browser already has (`Cache-Control: private, no-cache`).

The files are not encrypted by the API. For encryption at rest, turn it on in the bucket (S3 does it by default); `ENCRYPTION_KEY` is for small secrets in the database (see [encryption.md](encryption.md)).

## Multiple instances

With `s3`, nothing changes: every instance reads and writes the same bucket. With `local`, the instances need the same directory (a shared volume), or each one would only find the files it received.

The cleanup runs in one instance at a time, like every periodic job.

## Tests

The API of the tests keeps the files in memory (`storage.NewMemory`). To look at what is stored, pass your own:

```go
st := storage.NewMemory()
api := testutil.NewAPI(pool, testutil.WithStorage(st))

w := testutil.Upload(t, api, cookie, "/files", "photo.png", testutil.PNG)
file := testutil.Decode[files.File](t, w, http.StatusCreated)
// st.Keys() has one key
```

`testutil.Upload` sends the form as a browser does. `testutil.PNG` and `testutil.PDF` are contents with the right first bytes; a test of a rejected type sends its own (`[]byte("<html>…")`).

The S3 store is tested against a small server that answers like S3 (`internal/storage/storage_test.go`). To try a real bucket, or a MinIO in Docker, set `S3_TEST_BUCKET` and the other `S3_TEST_*` variables (see `.env.example`): `TestS3Real` only runs with them, like the SMTP test with Mailpit.

[AWS Emulator](https://docs.aws.amazon.com/emulator/latest/userguide/getting-started-linux.html), the official one, serves S3 on your machine without an account. It writes its certificate and the keys of each run to `~/.aws/emulator/9297/`; the SDK trusts the certificate through `AWS_CA_BUNDLE`, and the bucket is created first (`aws s3api create-bucket --bucket files-test`, with the AWS CLI set up as its guide says):

```bash
AWS_CA_BUNDLE=~/.aws/emulator/9297/ca.pem S3_TEST_BUCKET=files-test S3_TEST_REGION=us-east-1 \
S3_TEST_ENDPOINT=https://127.0.0.1:9297 S3_TEST_PATH_STYLE=true \
S3_TEST_ACCESS_KEY_ID=… S3_TEST_SECRET_ACCESS_KEY=… go test -run TestS3Real ./internal/storage/
```

The same variables without `_TEST`, with `STORAGE_DRIVER=s3`, point the API of development at it.

## What it does not include

- **Thumbnails or resizing images.** It needs an image library, and each project wants different sizes. A job can make them: it reads the file from the store and writes another one.
- **Uploading straight from the browser to S3** with a signed URL. It saves the API the traffic of very large files (videos), at the cost of a second flow, CORS rules in the bucket and checking the file after the fact. Under some tens of megabytes, going through the API is simpler and just as good.
- **Part of a file from S3** (a `Range` request): the local store answers it, S3 sends the file whole. It matters for seeking in a video.
- **Antivirus.** If the users exchange files with each other, scan them in a job before letting others download them.
- **Public URLs or a CDN.** Every file goes through the permission check. A project with public images puts them in a public bucket of its own.

## Removing file uploads

**Only S3**, keeping the local disk:

1. Delete `internal/storage/s3.go`. In `internal/storage/storage_test.go`, delete `fakeS3`, `TestS3` and `TestS3Real`, and the imports left unused.
2. In `cmd/api/main.go`, `newStorage` only returns `storage.NewLocal(c.Dir)`, and its `ctx` parameter goes.
3. In `internal/config`, remove `StorageDriverS3`, the `S3…` fields of `Storage` and what loads and checks them in `loadStorage`; in `config_test.go`, the S3 part of `TestLoadStorage` and the S3 cases of `TestLoadInvalid`. Remove the `S3_*` variables from `.env.example`, `deploy/.env.example`, the README and this guide.
4. `go mod tidy`, which removes the AWS SDK, and `make check`.

**Everything:**

1. Delete `internal/files`, `internal/storage`, the `00016_create_files.sql` and `00017_create_note_attachments.sql` migrations, and the `files.sql` query. In `internal/db/queries/notes.sql`, delete from `GetNoteID` to the end.
2. In `internal/notes`, delete `attachments.go` and `attachments_test.go`. In `routes.go`, remove the three attachment routes and the `uploads` parameter. In `handlers.go`, remove the `files` and `db` fields with their parameters in `NewHandler`, the line about files in the package comment, and in `delete` put the `DeleteNote` call back in place of the transaction.
3. In `internal/server/routes.go`, remove the `Storage` field of `Deps`, the block that creates `filesService` and `uploads` and registers the routes, and the extra arguments of `notes.NewHandler` and `notes.Register`.
4. In `internal/jobs`, remove the `Storage` field of `Deps` (`workers.go`), the `storage` field of `cleanupWorker` with the block that calls `files.Purge` and its line in the `CleanupArgs` comment (`cleanup.go`), and `TestCleanupDeletesUnusedFiles`. In `cmd/api/main.go`, remove `newStorage` and its two uses.
5. In `internal/config`, remove `Storage`, its constants and `loadStorage`, with `TestLoadStorage` and the storage cases of `TestLoadInvalid`. Remove the `STORAGE_*` and `S3_*` variables from `.env.example` and the README, and `/storage/` from `.gitignore`.
6. In `internal/testutil/http.go`, remove `WithStorage`, `Upload`, `PNG` and `PDF`.
7. Remove the keys `errors.file_in_use`, `errors.file_not_found`, `errors.malformed_upload`, `errors.storage_quota_exceeded`, `validation.file_too_large` and `validation.file_type` from the catalogs, and the four codes from the table of [i18n.md](i18n.md#error-codes).
8. `make sqlc`, `go mod tidy` and `make check`, which names the routes left in `internal/openapi/openapi.yaml` without code: delete their operations (`/files…`, `/notes/{id}/attachments…`) and the `File` schema.
9. What the deployment has for the files: in the `Dockerfile`, the `/data/storage` directory and `STORAGE_DIR`; in `deploy/compose.yaml`, the `storage` volume; in `deploy/.env.example`, the storage block; in [deploy.md](deploy.md), the backup of the files. And the "Files" section of `AGENTS.md`, with its rows in the table of "Where to look".

Both recipes were tested on a copy of the project, with a test database created only with the remaining migrations.
