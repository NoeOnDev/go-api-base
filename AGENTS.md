# AGENTS.md

Instrucciones para agentes de código (Claude Code, Codex, Copilot) que trabajen en este proyecto.

## Qué es

> **Este repositorio es una plantilla**, no una dependencia: `gonew` copia el código y los proyectos creados no quedan
> conectados a ella. Por eso acá se puede reorganizar libremente (incluidas las migraciones, que son el esquema inicial).
> Cada cambio de la plantilla se anota en `CHANGELOG.md`, con el SQL para llevar los cambios de esquema a un proyecto
> ya desplegado.
>
> **Migraciones en un proyecto creado con la plantilla:** hasta el primer despliegue, un campo nuevo o eliminado se
> cambia **en la migración original** (y se recrea la base local). Después del primer despliegue, siempre con una
> migración nueva.

`go-api-base` es una plantilla de API REST en Go, completa y lista para producción, para proyectos personales, de
clientes y de empresa. Corre como un solo binario con PostgreSQL, con una instancia o con varias (ver "Varias instancias"
en `docs/decisions.md`).
Prioriza la librería estándar y suma solo lo esencial: PostgreSQL (pgx), migraciones (goose), consultas tipadas (sqlc),
la cola de trabajos (River) y paquetes del equipo de Go (`golang.org/x/crypto`, `x/time`, `x/term`).

Trae autenticación completa (sesiones con cookie, roles `user`/`admin`, perfil, cambio y recuperación de contraseña,
verificación de email opcional, 2FA con TOTP, sesiones por dispositivo, cambio de email y borrado de cuenta; ver `docs/auth.md`), envío de correos, trabajos en segundo plano con River
(`docs/jobs.md`), tiempo real con SSE y notificaciones (`docs/realtime.md`), y tres **recursos de ejemplo** que sirven de modelo:

| Recurso | Qué muestra |
|---|---|
| `internal/notes` | CRUD protegido con **pertenencia**: cada usuario ve solo lo suyo |
| `internal/categories` | Lectura pública, escritura solo admin, valor único (409), no borrar si está en uso (409) |
| `internal/products` | Relación con otra tabla (JOIN), filtros y búsqueda, FK inexistente (422), datos visibles según el rol |

## Comandos

| Comando | Para qué |
|---|---|
| `make run` | Levanta la API (aplica las migraciones al arrancar) |
| `make check` | **Correr siempre antes de commitear**: gofmt, sqlc diff, go vet, staticcheck, go test y govulncheck |
| `make test` | Tests (`go test -p 1`; los de base de datos usan `TEST_DATABASE_URL`, sin ella se saltean) |
| `make sqlc` | Regenerar `internal/store` después de cambiar migraciones o consultas |
| `make migrate-new name=create_x` | Crear una migración nueva |
| `make migrate-up` / `migrate-down` / `migrate-status` | Manejar migraciones en desarrollo |
| `make create-admin email=… name=…` | Crear un administrador (contraseña por `ADMIN_PASSWORD` o por la terminal) |
| `make key` / `make rotate-keys` | Generar una `ENCRYPTION_KEY` / terminar una rotación (ver `docs/encryption.md`) |
| `make river-migration version=N` | SQL de una migración nueva de River, al actualizarlo (ver `docs/jobs.md`) |
| `make build` | Binario estático en `bin/api` |

`make` carga `.env` automáticamente. Las herramientas (sqlc, goose, staticcheck, govulncheck) se ejecutan con `go run paquete@versión`,
con las versiones fijas al principio del `Makefile`; no hace falta instalarlas. **No crear subcarpetas con su propio `go.mod`**:
`gonew` no las copia.

## Estructura

```
cmd/api/              arranque (main.go) y subcomando create-admin (admin.go)
internal/config/      configuración desde variables de entorno
internal/httpx/       JSON, errores, paginación, middlewares, CORS, límite de peticiones por IP, IP del cliente
internal/server/      http.Server con timeouts, rutas, CSRF y orden de middlewares (routes.go)
internal/auth/        usuarios, contraseñas (argon2id), sesiones, RequireUser/RequireAdmin, perfil,
                      recuperación y verificación; plantillas de correo en emails/*.txt
internal/ratelimit/   límites de intentos por cuenta o usuario, guardados en PostgreSQL (valen para todas las instancias)
internal/mail/        envío de correos: LogSender, SMTPSender y Recorder (tests)
internal/jobs/        trabajos en segundo plano con River: cliente, workers (workers.go), periódicos, MailSender
internal/realtime/    tiempo real con SSE: Stream, Broker (canales y temas), GET /events y LISTEN/NOTIFY entre instancias
internal/notifications/ notificaciones guardadas y enviadas en vivo; auth las usa para los avisos de seguridad
internal/totp/        códigos TOTP del 2FA (RFC 6238), solo librería estándar
internal/secretbox/   llavero: cifrado AES-256-GCM y HMAC con la clave actual y las anteriores (rotación)
internal/db/          pool de pgx, migraciones, errores de PostgreSQL y consultas SQL (queries/*.sql)
internal/store/       GENERADO por sqlc: no editar a mano
internal/validate/    reglas de validación compartidas (obligatorio, longitud, slug, no negativo)
internal/<recurso>/   un paquete por recurso: handlers.go, validation.go, routes.go, handlers_test.go
internal/testutil/    ayudas para tests de integración (NewDB, Login, NewAPI, Do, Decode)
migrations/           migraciones SQL de goose, embebidas en el binario
docs/                 guías (recursos, autenticación) y decisiones de diseño
```

## Convenciones

**Generales**
- Go idiomático y simple. Preferir la librería estándar; **no agregar dependencias sin consultarlo**.
- Comentarios y mensajes al cliente en español. Nombres de código en inglés.
- Errores envueltos con contexto: `fmt.Errorf("crear nota: %w", err)`. No usar `panic` para errores normales.
- Pasar `r.Context()` a todo lo que hable con la base.

**HTTP**
- Los handlers tienen la firma `func(w, r) error` y se registran con `httpx.Handle`. Ante un error, solo `return err`.
- Leer JSON con `httpx.Decode` y responder con `httpx.Write`. Nunca escribir JSON a mano.
- Errores para el cliente: `httpx.BadRequest`, `NotFound`, `Unauthorized`, `Forbidden`, `Conflict`, `Validation(fields)`.
  Cualquier otro error se responde como 500 sin exponer detalles.
- Paginación con `httpx.ParsePage` y respuesta `httpx.List[T]` (`{data, meta}`). IDs de la ruta con `httpx.PathID(r, "id")`.
- Cada recurso define su propio struct de respuesta; no devolver directamente los structs de `internal/store`.
- Logs con `httpx.LoggerFrom(r.Context())` (ya incluye `request_id` y, con sesión, `user_id`). Para sumar un dato a todas las
  líneas de la petición, incluida la final, usar `httpx.AddLogAttrs(ctx, "clave", valor)`. No registrar contraseñas ni tokens.
  Una ruta que se consulta muy seguido (un health check, métricas) se envuelve con `httpx.Quiet`: su línea va en `debug`, salvo los 5xx.

**Autenticación y permisos**
- Proteger rutas en `routes.go` del recurso: `authn.RequireUser(...)` (cualquier usuario), `authn.RequireAdmin(...)`
  o `authn.OptionalUser(...)` (ruta pública que muestra más a ciertos usuarios; en el handler, `auth.UserFrom`).
- En un handler protegido, el usuario es `auth.MustUser(r.Context())`.
- **Datos de cada usuario:** la tabla referencia `users (id)` con `ON DELETE CASCADE` (o `SET NULL` si deben quedar sin
  dueño); con `RESTRICT`, borrar la cuenta (`DELETE /auth/me`) falla.
- **Pertenencia:** si los datos son de cada usuario, la tabla lleva `user_id` y **todas** las consultas filtran por él
  (`WHERE id = $1 AND user_id = $2`). Un recurso ajeno responde 404, no 403. Ver `internal/notes`.
- No tocar la protección CSRF ni CORS de `internal/server/routes.go` sin consultarlo.
- `RequireUser` también exige el email verificado si `REQUIRE_EMAIL_VERIFICATION=true` (403 `email_not_verified`).

**Límites de intentos y varias instancias** (ver "Límites de intentos" y "Varias instancias" en `docs/decisions.md`)
- Por IP, en memoria: `httpx.NewRateLimiter(every, burst).MiddlewareByIP(proxies)`. Cada instancia cuenta por separado.
- Por cuenta o por usuario, en PostgreSQL: `ratelimit.New(queries, "nombre", every, burst)`, con `Allow(ctx, clave)` o
  `Middleware(clave)`. Vale para todas las instancias: lo que frena la fuerza bruta contra una cuenta va siempre acá.
- Nada que tenga que valer para todas las instancias (un contador, una caché, un "ya se hizo") se guarda en memoria:
  va en PostgreSQL.

**Datos sensibles**
- Todo dato secreto que haya que guardar y después leer (como el secreto del 2FA o un token de una API externa) se
  cifra con `secretbox.Box.Encrypt(dato, aad)`. `aad` ata el dato a su fila, por ejemplo `"users.totp_secret:<id>"`
  (ver `totpAAD` en `internal/auth/two_factor.go`); `Decrypt` necesita el mismo. Lo que solo se compara (como los
  códigos de recuperación) se guarda con `Box.MAC` sobre el valor y el id de su dueño, se verifica con `Box.MACs`
  (todas las claves) y se guarda también `Box.KeyID()`.
- Si agregás un dato cifrado nuevo, sumalo a `auth.RotateKeys` (o a su equivalente) para que la rotación lo incluya.
- Nunca guardar secretos en texto plano ni registrarlos en el log.
- Las contraseñas se hashean solo con `auth.HashPassword(ctx, …)` y se comparan con `auth.VerifyPassword(ctx, …)`:
  limitan cuántos hashes de argon2 corren a la vez. No llamar a `argon2` directamente.
- Las respuestas con datos personales o secretos llevan `Cache-Control: no-store`: envolver la ruta con `httpx.NoStore`
  (todo `/auth` ya lo tiene).

**Correos**
- Enviarlos con un `mail.Sender` (en `auth`, con `s.sendEmail` y una plantilla de `internal/auth/emails`). Nunca bloquear la respuesta
  esperando el envío: en producción el sender es un `jobs.MailSender`, que los encola con reintentos.
- Los enlaces de los correos apuntan al frontend (`FRONTEND_URL`). Los tokens que viajan por correo son de un solo uso, vencen
  y se guardan hasheados (ver `internal/auth/tokens.go`).

**Trabajos en segundo plano** (ver `docs/jobs.md`)
- Cada trabajo es un struct de argumentos con `Kind()` y un worker con `river.WorkerDefaults`. Registrar el worker en
  `registerWorkers` y los periódicos en `periodicJobs` (`internal/jobs/workers.go`). No cambiar un `Kind` ya desplegado.
- Si el trabajo depende de datos que se guardan en la misma petición, encolarlo con `InsertTx` dentro de la transacción.
- Los argumentos se guardan sin cifrar: preferir ids, y cifrar con `secretbox` cualquier secreto o dato personal (como `SendEmailArgs`).
- Un worker puede ejecutarse dos veces (reintentos, rescate de trabajos cortados): tiene que tolerarlo.
- `internal/store` trae tipos de las tablas de River (`RiverJob`…): no usarlos; a River se le habla con su cliente.

**Tiempo real** (ver `docs/realtime.md`)
- Publicar con `broker.PublishToUser` (datos del usuario) o `broker.Publish` en un tema (datos compartidos), **después**
  de guardar (y del `Commit`). Para una notificación que quede guardada, `notifications.Service.Notify`.
- Lo publicado llega a las conexiones de todas las instancias (`LISTEN/NOTIFY`, en `internal/realtime/instances.go`), sin
  que quien publica haga nada distinto. Un evento de más de 8 KB viaja por la tabla `realtime_messages`.
- Cada tipo de tema nuevo necesita `broker.Authorize("tipo", …)` con el control de permiso; sin eso, se rechaza.
  Al quitarle a alguien el acceso, llamar a `broker.DisconnectUser` para que deje de escuchar en el acto.
- Para transmitir una respuesta larga (un agente de IA), `realtime.Open` y `stream.Send`. Después de `Open` los errores
  van como evento, no como JSON.

**Base de datos**
- Todo el SQL vive en `internal/db/queries/*.sql`. Después de cambiarlo, correr `make sqlc`.
- **Nunca editar `internal/store`**: se regenera.
- **Antes del primer despliegue**, cambiar las migraciones originales en lugar de sumar `ALTER TABLE` (y recrear la base local).
  **Después**, nunca modificar una migración ya aplicada: crear una nueva con `make migrate-new`.
- Cada migración debe tener su sección `-- +goose Down`.
- Una fila inexistente llega como `pgx.ErrNoRows` (`:one`) o 0 filas afectadas (`:execrows`): responder `httpx.NotFound`.
- Varias escrituras que van juntas, en una transacción con `pgx.BeginFunc` y `q.WithTx(tx)` (ver `docs/adding-a-resource.md`).
  Nada lento adentro (argon2, llamadas externas); los avisos (eventos, `SessionWatcher`) después del commit. Si dos
  peticiones simultáneas pueden pisarse, bloquear la fila con `SELECT … FOR UPDATE` (como `LockUser`).
- Valor único repetido: `db.IsUniqueViolation(err)` → `httpx.Conflict`. FK inexistente o en uso:
  `db.IsForeignKeyViolation(err)` → `httpx.Validation` (al crear/editar) o `httpx.Conflict` (al borrar).
- Filtros opcionales en sqlc: `sqlc.narg('x')::tipo IS NULL OR columna = sqlc.narg('x')`. Escapar `%` y `_` en búsquedas `ILIKE`.
  `ILIKE '%…%'` no usa índices: si la tabla crece, `pg_trgm` (ver `docs/adding-a-resource.md`).
- Dinero en centavos (`bigint`), nunca `float`.

**Tests**
- Tests de integración con `testutil.NewDB(t)` (vacía todas las tablas) y `testutil.Login(t, pool, email, rol)` para la sesión.
  Usan solo `TEST_DATABASE_URL`, que debe terminar en `_test` y ser distinta de `DATABASE_URL`. **No quitar esa protección.**
- Peticiones con `testutil.NewAPI`, `testutil.Do` y `testutil.Decode`, como en `internal/products/handlers_test.go`.
  Para leer los correos enviados: `testutil.WithMailer(&mail.Recorder{})` y `testutil.TokenFromEmail`. Para cambiar la
  configuración: `testutil.WithConfig(func(c *config.Config){ … })`. Para el 2FA: `testutil.EnableTwoFactor(t, api, cookie, testutil.Password)` y `TwoFactor.Code`.
- Las rutas de auth tienen límites por IP (ver `internal/server/routes.go`; el más bajo es de 10 seguidos) y las acciones
  de cuenta, de 5 por usuario. Con `testutil.Do` todas las peticiones salen de la misma IP; para más, o para simular
  varias IPs, usar `testutil.DoFromIP`.
- Si la API del test cambia la configuración de la cookie (`WithConfig` con `CookieSecure`), pasarle las mismas
  opciones a `testutil.Login` para que la cookie coincida.
- Eventos en tiempo real: `testutil.NewServer` (un servidor HTTP real) y `testutil.OpenEvents`, que devuelve un lector con
  `Next`, `NextComment` y `Nothing`. Para publicar desde el test, `testutil.WithBroker`. Ver `internal/notes/events_test.go`.
- Workers: `rivertest.NewWorker` y `rivertest.RequireInserted`. Ver `internal/jobs/email_test.go`.
- El envío real por SMTP (`TestSMTPSender`) solo corre con `SMTP_TEST_ADDR`, por ejemplo `SMTP_TEST_ADDR=localhost:1025`
  con Mailpit (los correos se ven en `http://localhost:8025`). Sin ella se saltea; en CI no corre.
- Los tests que usan la base no llevan `t.Parallel()`, y los paquetes corren en serie (`-p 1`, ya incluido en `make test`).
- Cubrir siempre: el caso feliz, validación (422), no encontrado (404), sin sesión (401) y, si aplica, sin permiso (403) o pertenencia.

## Agregar un recurso

Seguir `docs/adding-a-resource.md`: migración → consultas → `make sqlc` → paquete del recurso
(handlers, validación, rutas, tests) → registrar en `internal/server/routes.go` → `make check`.
Usar `internal/notes` como modelo si los datos son de cada usuario, o `internal/categories`/`products` si son compartidos.

## Notas para el MCP de gopls

Iniciar la sesión en la raíz del proyecto (donde está `go.mod`), para que `go_workspace` detecte el módulo.
Las instrucciones siguientes son las oficiales de gopls (`gopls mcp -instructions`).

---

# The gopls MCP server

These instructions describe how to efficiently work in the Go programming language using the gopls MCP server. You can load this file directly into a session where the gopls MCP server is connected.

## Detecting a Go workspace

At the start of every session, you MUST use the `go_workspace` tool to learn about the Go workspace. ONLY if you are in a Go workspace, you MUST run `go_vulncheck` immediately afterwards to identify any existing security risks. The rest of these instructions apply whenever that tool indicates that the user is in a Go workspace.

## Go programming workflows

These guidelines MUST be followed whenever working in a Go workspace. There are two workflows described below: the 'Read Workflow' must be followed when the user asks a question about a Go workspace. The 'Edit Workflow' must be followed when the user edits a Go workspace.

You may re-do parts of each workflow as necessary to recover from errors. However, you must not skip any steps.

### Read workflow

The goal of the read workflow is to understand the codebase.

1. **Understand the workspace layout**: Start by using `go_workspace` to understand the overall structure of the workspace, such as whether it's a module, a workspace, or a GOPATH project.

2. **Find relevant symbols**: If you're looking for a specific type, function, or variable, use `go_search`. This is a fuzzy search that will help you locate symbols even if you don't know the exact name or location.
   EXAMPLE: search for the 'Server' type: `go_search({"query":"server"})`

3. **Understand a file and its intra-package dependencies**: When you have a file path and want to understand its contents and how it connects to other files *in the same package*, use `go_file_context`. This tool will show you a summary of the declarations from other files in the same package that are used by the current file. `go_file_context` MUST be used immediately after reading any Go file for the first time, and MAY be re-used if dependencies have changed.
   EXAMPLE: to understand `server.go`'s dependencies on other files in its package: `go_file_context({"file":"/path/to/server.go"})`

4. **Understand a package's public API**: When you need to understand what a package provides to external code (i.e., its public API), use `go_package_api`. This is especially useful for understanding third-party dependencies or other packages in the same monorepo.
   EXAMPLE: to see the API of the `storage` package: `go_package_api({"packagePaths":["example.com/internal/storage"]})`

### Editing workflow

The editing workflow is iterative. You should cycle through these steps until the task is complete.

1. **Read first**: Before making any edits, follow the Read Workflow to understand the user's request and the relevant code.

2. **Find references**: Before modifying the definition of any symbol, use the `go_symbol_references` tool to find all references to that identifier. This is critical for understanding the impact of your change. Read the files containing references to evaluate if any further edits are required.
   EXAMPLE: `go_symbol_references({"file":"/path/to/server.go","symbol":"Server.Run"})`

3. **Make edits**: Make the required edits, including edits to references you identified in the previous step. Don't proceed to the next step until all planned edits are complete.

4. **Check for errors**: After every code modification, you MUST call the `go_diagnostics` tool. Pass the paths of the files you have edited. This tool will report any build or analysis errors.
   EXAMPLE: `go_diagnostics({"files":["/path/to/server.go"]})`

5. **Fix errors**: If `go_diagnostics` reports any errors, fix them. The tool may provide suggested quick fixes in the form of diffs. You should review these diffs and apply them if they are correct. Once you've applied a fix, re-run `go_diagnostics` to confirm that the issue is resolved. It is OK to ignore 'hint' or 'info' diagnostics if they are not relevant to the current task. Note that Go diagnostic messages may contain a summary of the source code, which may not match its exact text.

6. **Check for vulnerabilities**: If your edits involved adding or updating dependencies in the go.mod file, you MUST run a vulnerability check on the entire workspace. This ensures that the new dependencies do not introduce any security risks. This step should be performed after all build errors are resolved. EXAMPLE: `go_vulncheck({"pattern":"./..."})`

7. **Run tests**: Once `go_diagnostics` reports no errors (and ONLY once there are no errors), run the tests for the packages you have changed. You can do this with `go test [packagePath...]`. Don't run `go test ./...` unless the user explicitly requests it, as doing so may slow down the iteration loop.
