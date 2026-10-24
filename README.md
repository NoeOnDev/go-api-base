# go-api-base

Plantilla de API REST en Go, liviana y lista para producción en un VPS chico.
Usa la librería estándar más tres piezas: **PostgreSQL con pgx**, **migraciones con goose** y **consultas tipadas con sqlc**.

Incluye:

- Configuración por variables de entorno.
- Servidor HTTP con timeouts y apagado limpio.
- Logs con `slog` y un `request_id` por petición.
- Middlewares de request ID, logging y recuperación de panics.
- Formato único de errores en JSON.
- Validación y paginación.
- Migraciones embebidas que se aplican al arrancar.
- Health check.
- Tests de integración.
- Un recurso de ejemplo completo (`notes`) para copiar.

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

3. **Levantar la API:**

   ```bash
   make run
   ```

   Al arrancar aplica las migraciones y queda escuchando en `:8080`.

4. **Probarla:**

   ```bash
   curl localhost:8080/healthz
   curl -X POST localhost:8080/notes -d '{"title":"Mi primera nota","body":"hola"}'
   curl localhost:8080/notes
   ```

## Endpoints de ejemplo

| Método | Ruta | Respuesta |
|---|---|---|
| `GET` | `/healthz` | `200` si la API y la base responden, `503` si la base no responde |
| `GET` | `/notes?limit=20&offset=0` | `200` con `{"data": [...], "meta": {"limit", "offset", "total"}}` |
| `POST` | `/notes` | `201` con la nota creada, `422` si no pasa la validación |
| `GET` | `/notes/{id}` | `200` o `404` |
| `PUT` | `/notes/{id}` | `200`, `404` o `422` |
| `DELETE` | `/notes/{id}` | `204` o `404` |

Todos los errores tienen la misma forma:

```json
{"error": {"code": "validation_failed", "message": "los datos enviados no son válidos", "fields": {"title": "es obligatorio"}}}
```

## Comandos

Ejecutá `make` para ver la lista completa. Los principales:

| Comando | Para qué |
|---|---|
| `make run` | Levanta la API |
| `make check` | gofmt, sqlc al día, go vet, tests y govulncheck. Correlo antes de cada commit |
| `make sqlc` | Regenera el código de la base después de cambiar migraciones o consultas |
| `make migrate-new name=create_x` | Crea una migración |
| `make build` | Binario estático en `bin/api` |

## Estructura

```
cmd/api/            arranque de la aplicación
internal/config/    variables de entorno
internal/httpx/     JSON, errores, paginación y middlewares
internal/server/    servidor y rutas
internal/db/        conexión, migraciones y consultas SQL
internal/store/     código generado por sqlc (no editar)
internal/notes/     recurso de ejemplo
migrations/         migraciones SQL
tools/              herramientas de desarrollo (sqlc, goose, govulncheck)
docs/               guías y decisiones
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

Después reemplazá `notes` por tus recursos siguiendo [docs/adding-a-resource.md](docs/adding-a-resource.md).

## Documentación

- [Cómo agregar un recurso](docs/adding-a-resource.md)
- [Decisiones de diseño](docs/decisions.md)
- [AGENTS.md](AGENTS.md): convenciones para agentes de código (Claude Code, Codex, Copilot)
