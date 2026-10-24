# Cambios

Historial de versiones de la plantilla. Cada proyecto creado con `gonew` es una copia independiente:
usá esta lista para saber de qué versión salió un proyecto y qué mejoras podrías llevarle a mano.

Formato: una sección por versión, con lo **agregado**, **cambiado** y **corregido**.

## [v0.1.0] - 2026-10-23

Primera versión publicada.

### Agregado

- **Base de la API:**
  - `net/http` con rutas por método, timeouts del servidor y apagado limpio con SIGINT/SIGTERM.
  - Configuración por variables de entorno, validada al arrancar.
  - Logs con `slog` (texto en desarrollo, JSON en producción), con `request_id` y `user_id` en cada línea de la petición.
  - Formato único de errores JSON, lectura estricta de JSON (límite de 1 MB y sin campos desconocidos) y paginación `{data, meta}`.
  - Middlewares de request ID, logging y recuperación de panics. `/healthz` con ping a la base.
- **Base de datos:**
  - PostgreSQL con `pgx`.
  - Consultas tipadas con `sqlc`.
  - Migraciones de `goose` embebidas en el binario y aplicadas al arrancar, con un bloqueo para que dos instancias no migren a la vez.
  - Traducción de errores de PostgreSQL: un valor único repetido o una FK inválida se responden como 409 o 422.
- **Autenticación:**
  - Registro, login, logout y `/auth/me`, con sesiones en PostgreSQL y una cookie `HttpOnly` y `SameSite=Lax`. En la base solo se guarda el SHA-256 del token.
  - Contraseñas con argon2id (parámetros de OWASP). El login no revela qué emails existen.
  - Roles `user` y `admin`, con los middlewares `RequireUser`, `RequireAdmin` y `OptionalUser`.
  - Subcomando `create-admin` (`make create-admin`).
- **Seguridad:**
  - Protección CSRF con `http.CrossOriginProtection`.
  - CORS configurable.
  - Límite de 5 intentos por minuto en el login y el registro, por IP o por bloque `/64` en IPv6.
- **Recursos de ejemplo:**
  - `notes`: datos de cada usuario, donde el recurso ajeno responde 404.
  - `categories`: lectura pública, escritura de admin, slug único y no se puede borrar si tiene productos.
  - `products`: JOIN con la categoría, filtros y búsqueda, precio en centavos, y productos inactivos visibles solo para el admin.
- **Tests:**
  - Tests de integración contra una base de pruebas separada, que se niega a correr si no termina en `_test` o si es la de la app.
  - Helpers `NewDB`, `Login`, `NewAPI`, `Do` y `Decode`.
- **Herramientas:**
  - `Makefile` con `run`, `build`, `check` (gofmt, sqlc al día, vet, tests y govulncheck), migraciones y `create-admin`.
  - Herramientas con versión fija mediante `go run paquete@versión`.
  - CI en GitHub Actions con PostgreSQL 18.
- **Documentación:** `README`, `AGENTS.md` (con las instrucciones del MCP de gopls), y en `docs/` la guía de autenticación, la de recursos y las decisiones de diseño.
