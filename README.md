# go-api-base

Plantilla de API REST en Go, liviana y lista para producción en un VPS chico. Usa la librería estándar más:
- **PostgreSQL con pgx**
- **migraciones con goose**
- **consultas tipadas con sqlc**
- paquetes oficiales del equipo de Go para las contraseñas y el límite de peticiones

Incluye:

- **Autenticación** con sesiones en cookie `HttpOnly`, contraseñas con argon2id y roles `user`/`admin`. El primer admin se crea con un comando.
- **Seguridad:** protección CSRF, CORS configurable, límite de intentos en el login y timeouts del servidor.
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

2. **Configurar el entorno.** Copiá el archivo de ejemplo y completá `DATABASE_URL` y `TEST_DATABASE_URL`:

   ```bash
   cp .env.example .env
   ```

3. **Crear el administrador.** El comando aplica las migraciones y te pide la contraseña:

   ```bash
   make create-admin email=admin@ejemplo.com name="Admin"
   ```

4. **Levantar la API:**

   ```bash
   make run
   ```

5. **Probarla.** `-c` y `-b` guardan y envían la cookie de sesión:

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
| `GET` | `/auth/me` | Con sesión |
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
| `TRUST_PROXY` | `false` | Leer la IP real de `X-Forwarded-For` (con Caddy o Nginx delante) |

## Comandos

Ejecutá `make` para ver la lista completa. Los principales:

| Comando | Para qué |
|---|---|
| `make run` | Levanta la API |
| `make check` | gofmt, sqlc al día, go vet, tests y govulncheck. Correlo antes de cada commit |
| `make sqlc` | Regenera el código de la base después de cambiar migraciones o consultas |
| `make migrate-new name=create_x` | Crea una migración |
| `make create-admin email=…` | Crea un administrador |
| `make build` | Binario estático en `bin/api` |

## Estructura

```
cmd/api/             arranque de la aplicación y comando create-admin
internal/config/     variables de entorno
internal/httpx/      JSON, errores, paginación, middlewares, CORS y límite de peticiones
internal/server/     servidor, rutas y protección CSRF
internal/auth/       usuarios, sesiones y roles
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
- [Cómo agregar un recurso](docs/adding-a-resource.md)
- [Decisiones de diseño](docs/decisions.md)
- [AGENTS.md](AGENTS.md): convenciones para agentes de código (Claude Code, Codex, Copilot).
