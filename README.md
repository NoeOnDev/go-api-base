# go-api-base

Plantilla de API REST en Go, liviana y lista para producción en un VPS chico. Usa la librería estándar más:
- **PostgreSQL con pgx**
- **migraciones con goose**
- **consultas tipadas con sqlc**
- paquetes oficiales del equipo de Go para las contraseñas y el límite de peticiones

Incluye:

- **Autenticación** con sesiones en cookie `HttpOnly`, contraseñas con argon2id y roles `user`/`admin`. Incluye perfil, cambio de contraseña, recuperación por correo, verificación de email opcional y 2FA con app de autenticación y códigos de recuperación. El primer admin se crea con un comando.
- **Correos** por SMTP (cualquier proveedor), enviados en segundo plano, con plantillas editables. En desarrollo se usa Mailpit o el log.
- **Seguridad:** protección CSRF, CORS configurable, límites de intentos (por IP, por cuenta en el login y el 2FA, y por destinatario en los correos), argon2 con tope de concurrencia, cookies `__Host-` en producción, encabezados `nosniff` y `no-store`, y timeouts del servidor.
- **Base de la API:**
  - formato único de errores en JSON
  - validación y paginación
  - logs con `slog` y un `request_id` por petición
  - migraciones embebidas que se aplican al arrancar
  - apagado limpio
- **Tres recursos de ejemplo** para copiar:
  - `notes`: datos de cada usuario
  - `categories` y `products`: un catálogo con relaciones, filtros y permisos de admin
- **Calidad:**
  - tests de integración contra PostgreSQL
  - `govulncheck`
  - CI con GitHub Actions

## Requisitos

- Go 1.27.3 o más nuevo.
- PostgreSQL, en cualquier versión con soporte.
- `make`.
- `gcc`, solo para compilar sqlc la primera vez (`make sqlc`).

## Inicio rápido

1. **Crear la base de datos.** Este ejemplo usa un contenedor de PostgreSQL llamado `postgres_db`; adaptalo a tu instalación:

   ```bash
   docker exec -i postgres_db psql -U postgres <<'SQL'
   CREATE ROLE go_api_base LOGIN PASSWORD 'cambiar-esta-contraseña';
   CREATE DATABASE go_api_base OWNER go_api_base;
   CREATE DATABASE go_api_base_test OWNER go_api_base;
   SQL
   ```

2. **Configurar el entorno.** Copiá el archivo de ejemplo, completá `DATABASE_URL` y `TEST_DATABASE_URL`, y generá la clave de cifrado:

   ```bash
   cp .env.example .env
   sed -i "s|^ENCRYPTION_KEY=.*|ENCRYPTION_KEY=$(make -s key)|" .env
   ```

3. **(Opcional) Correos en desarrollo.** Levantá Mailpit, que muestra los correos en `http://localhost:8025`, y configurá `MAIL_DRIVER=smtp`, `SMTP_HOST=localhost` y `SMTP_PORT=1025`:

   ```bash
   docker run -d --name mailpit -p 1025:1025 -p 8025:8025 axllent/mailpit:v1.31.6
   ```

   Sin Mailpit, con `MAIL_DRIVER=log`, los correos (y sus enlaces) aparecen en el log de la API.

4. **Crear el administrador.** El comando aplica las migraciones y te pide la contraseña:

   ```bash
   make create-admin email=admin@ejemplo.com name="Admin"
   ```

5. **Levantar la API:**

   ```bash
   make run
   ```

6. **Probarla.** `-c` y `-b` guardan y envían la cookie de sesión:

   ```bash
   curl -c cookies.txt -X POST localhost:8080/auth/register \
     -d '{"name":"Ana","email":"ana@ejemplo.com","password":"una-clave-larga"}'
   curl -b cookies.txt -X POST localhost:8080/notes -d '{"title":"Mi primera nota"}'
   curl -b cookies.txt localhost:8080/notes
   curl 'localhost:8080/products?q=café'
   ```

## Endpoints

| Método | Ruta | Acceso |
|---|---|---|
| `GET` | `/healthz` | Público. `200` si la API y la base responden, `503` si no |
| `POST` | `/auth/register`, `/auth/login` | Público, con límite de intentos |
| `POST` | `/auth/logout` | Público; borra la sesión si existe y siempre borra la cookie |
| `GET`, `PUT` | `/auth/me` | Con sesión: ver y editar el perfil |
| `PUT` | `/auth/password` | Con sesión: cambiar la contraseña (pide la actual y cierra las demás sesiones) |
| `POST` | `/auth/password/forgot`, `/auth/password/reset` | Público: recuperar la contraseña por correo |
| `POST` | `/auth/email/verify`, `/auth/email/resend` | Verificar el email (obligatorio con `REQUIRE_EMAIL_VERIFICATION=true`) |
| `POST` | `/auth/two-factor/setup`, `/enable`, `/disable`, `/recovery-codes` | Con sesión: activar y administrar el 2FA |
| `POST` | `/auth/two-factor/challenge` | Segundo paso del login cuando el usuario tiene 2FA |
| `GET`, `POST` | `/notes` | Con sesión; cada usuario ve solo sus notas |
| `GET`, `PUT`, `DELETE` | `/notes/{id}` | Con sesión; la nota de otro usuario responde `404` |
| `GET` | `/categories`, `/categories/{id}` | Público |
| `POST`, `PUT`, `DELETE` | `/categories…` | Solo admin |
| `GET` | `/products?category_id=&q=&active=`, `/products/{id}` | Público: los visitantes solo ven productos activos; el admin ve todos |
| `POST`, `PUT`, `DELETE` | `/products…` | Solo admin |

Los listados aceptan `?limit=` (de 1 a 100, 20 por defecto) y `?offset=`, y responden `{"data": [...], "meta": {"limit", "offset", "total"}}`.

Todos los errores tienen la misma forma:

```json
{"error": {"code": "validation_failed", "message": "los datos enviados no son válidos", "fields": {"title": "es obligatorio"}}}
```

| Código | Cuándo |
|---|---|
| `400` | JSON mal formado o ID inválido |
| `401` | Sin sesión |
| `403` | Sin permiso, o petición de otro origen |
| `404` | El recurso no existe |
| `409` | Valor repetido, o el recurso está en uso |
| `422` | Validación |
| `429` | Demasiados intentos |
| `500` | Error interno, sin detalles; la causa queda en el log con su `request_id` |

## Configuración

Todo se configura con variables de entorno. [`.env.example`](.env.example) tiene la lista completa.

| Variable | Por defecto | Para qué |
|---|---|---|
| `APP_ENV` | `development` | `production` activa los logs en JSON y la cookie `Secure` |
| `HTTP_ADDR` | `:8080` | Dirección del servidor |
| `DATABASE_URL` | (obligatoria) | Conexión a PostgreSQL |
| `TEST_DATABASE_URL` | | Base de los tests de integración |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` o `error` |
| `SHUTDOWN_TIMEOUT` | `10s` | Espera máxima al apagar el servidor |
| `SESSION_TTL` | `168h` | Duración de la sesión |
| `COOKIE_SECURE` | `true` en producción | La cookie solo viaja por HTTPS |
| `CORS_ALLOWED_ORIGINS` | | Orígenes del frontend si está en otro dominio |
| `TRUST_PROXY` | `false` | Leer la IP real de `X-Forwarded-For` (con Caddy o Nginx delante). Solo se cree si la conexión viene de localhost o de una red privada |
| `FRONTEND_URL` | `http://localhost:5173` | Base de los enlaces de los correos |
| `REQUIRE_EMAIL_VERIFICATION` | `false` | Exigir el email verificado para usar las rutas protegidas |
| `MAIL_DRIVER` | `log` | `log` (escribe los correos en el log) o `smtp` |
| `MAIL_FROM` | `no-reply@localhost` | Remitente |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | — / `587` | Datos del servidor SMTP |
| `APP_NAME` | `Mi App` | Nombre que muestra la app de autenticación del 2FA |
| `ENCRYPTION_KEY` | (obligatoria) | Clave para cifrar el secreto del 2FA. Generala con `make key` (ver [docs/encryption.md](docs/encryption.md)) |
| `ENCRYPTION_PREVIOUS_KEYS` | | Claves anteriores, solo durante una rotación |
| `GOMEMLIMIT` | (sin límite) | Variable del propio Go, no de la plantilla. Con ella, el recolector de basura libera memoria antes de llegar a ese valor. En producción conviene cerca del 70 % de la RAM del servidor (con 1 GB, `700MiB`) |

Los valores con espacios van entre comillas: `APP_NAME="Mi App"`.

### Antes de producción

Lo que hay que revisar en la configuración, sea cual sea el servidor. Si `COOKIE_SECURE` o `MAIL_DRIVER` quedan mal en producción, la API lo avisa en el log al arrancar.

- **`APP_ENV=production`:** logs en JSON y cookies `Secure` con el prefijo `__Host-`.
- **HTTPS delante de la API** (por ejemplo, con Caddy o Nginx). Sin HTTPS, las cookies `Secure` no viajan y nadie puede iniciar sesión.
- **`TRUST_PROXY=true` solo si hay un proxy delante, y con el puerto de la API cerrado a internet.**
  - Con el proxy en el mismo servidor, que la API escuche solo ahí: `HTTP_ADDR=127.0.0.1:8080`.
  - Con Docker: si el proxy está en la misma red de Docker, no publiques el puerto de la API; si hace falta publicarlo, solo en localhost (`-p 127.0.0.1:8080:8080`). Si no, en algunas configuraciones las conexiones de afuera llegan con la IP privada del gateway de Docker, y se volvería a creer un `X-Forwarded-For` inventado.
- **`MAIL_DRIVER=smtp`** con un proveedor real, y **`FRONTEND_URL`** con el dominio del frontend (los enlaces de los correos apuntan ahí). Con el frontend en otro dominio, también `CORS_ALLOWED_ORIGINS`.
- **`ENCRYPTION_KEY` guardada como secreto y respaldada aparte de la base:** si se pierde, los usuarios tienen que volver a configurar el 2FA (ver [docs/encryption.md](docs/encryption.md)).
- **`GOMEMLIMIT`** cerca del 70 % de la RAM del servidor.

## Comandos

Ejecutá `make` para ver la lista completa. Los principales:

| Comando | Para qué |
|---|---|
| `make run` | Levanta la API |
| `make check` | gofmt, sqlc al día, go vet, tests y govulncheck. Correlo antes de cada commit |
| `make sqlc` | Regenera el código de la base después de cambiar migraciones o consultas |
| `make migrate-new name=create_x` | Crea una migración |
| `make create-admin email=…` | Crea un administrador |
| `make key` | Genera una clave para `ENCRYPTION_KEY` |
| `make rotate-keys` | Termina una rotación de `ENCRYPTION_KEY` (ver [docs/encryption.md](docs/encryption.md)) |
| `make build` | Binario estático en `bin/api` |

## Estructura

```
cmd/api/             arranque de la aplicación y comando create-admin
internal/config/     variables de entorno
internal/httpx/      JSON, errores, paginación, middlewares, CORS y límite de peticiones
internal/server/     servidor, rutas y protección CSRF
internal/auth/       usuarios, sesiones, roles, perfil, recuperación y verificación (y sus plantillas de correo)
internal/mail/       envío de correos (log, SMTP, en segundo plano)
internal/totp/       códigos TOTP del 2FA (RFC 6238)
internal/secretbox/  cifrado (AES-256-GCM) y HMAC con un llavero de claves rotables
internal/db/         conexión, migraciones, errores de PostgreSQL y consultas SQL
internal/store/      código generado por sqlc (no editar)
internal/validate/   reglas de validación compartidas
internal/notes/      ejemplo: datos de cada usuario
internal/categories/ ejemplo: catálogo, lectura pública y escritura de admin
internal/products/   ejemplo: relaciones, filtros y búsqueda
internal/testutil/   ayudas para los tests
migrations/          migraciones SQL
docs/                guías y decisiones
```

## Usar como base de un proyecto nuevo

**Con `gonew`** (si el repositorio está publicado):

```bash
go install golang.org/x/tools/cmd/gonew@latest
gonew github.com/NoeOnDev/go-api-base github.com/tu-usuario/mi-proyecto
```

`gonew` copia la plantilla y cambia el nombre del módulo y todos los imports.

**Copiando la carpeta** (si el repositorio es privado o no está publicado):

```bash
cp -r go-api-base mi-proyecto && cd mi-proyecto && rm -rf .git .env bin
go mod edit -module github.com/tu-usuario/mi-proyecto
grep -rl 'github.com/NoeOnDev/go-api-base' --include='*.go' --include='go.mod' . | xargs sed -i 's#github.com/NoeOnDev/go-api-base#github.com/tu-usuario/mi-proyecto#g'
git init
```

Después:
- Conservá la autenticación.
- Quedate con los ejemplos que se parezcan a tu proyecto y borrá el resto (su paquete, sus consultas, sus migraciones y su registro en `internal/server/routes.go`).
- Agregá tus recursos siguiendo [docs/adding-a-resource.md](docs/adding-a-resource.md).

## Documentación

- [Autenticación](docs/auth.md): cómo funciona y cómo usarla desde React.
- [Cifrado y `ENCRYPTION_KEY`](docs/encryption.md): qué protege, cómo guardarla y cómo rotarla.
- [Cómo agregar un recurso](docs/adding-a-resource.md)
- [Decisiones de diseño](docs/decisions.md)
- [AGENTS.md](AGENTS.md): convenciones para agentes de código (Claude Code, Codex, Copilot).
