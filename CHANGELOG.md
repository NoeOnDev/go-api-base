# Cambios

Historial de versiones de la plantilla. Cada proyecto creado con `gonew` es una copia independiente:
usá esta lista para saber de qué versión salió un proyecto y qué mejoras podrías llevarle a mano.

Formato: una sección por versión, con lo **agregado**, **cambiado** y **corregido**.

## [v0.3.0] - 2026-10-24

2FA voluntario con app de autenticación, comparable a lo que Laravel Fortify suma sobre Breeze.

### Agregado

- **2FA con TOTP (RFC 6238):** funciona con Google Authenticator, Authy, 1Password, etc.
  - `POST /auth/two-factor/setup` devuelve el secreto y la URL `otpauth://` para el QR.
  - `POST /auth/two-factor/enable` lo activa con un código válido y entrega 10 códigos de recuperación.
  - `POST /auth/two-factor/disable` y `POST /auth/two-factor/recovery-codes` piden la contraseña.
- **Login en dos pasos:** con 2FA, `POST /auth/login` responde `202 {two_factor_required: true}` y una cookie de desafío que vence en 5 minutos. `POST /auth/two-factor/challenge` acepta el código de la app o un código de recuperación.
- **Protecciones del 2FA:**
  - un código ya usado se rechaza;
  - cada código de recuperación sirve una vez;
  - al activar o desactivar el 2FA se cierran las demás sesiones;
  - recuperar la contraseña no saltea el 2FA.
- **Paquete `internal/totp`**, con la librería estándar y probado con los vectores del RFC.
- **Paquete `internal/secretbox`:** AES-256-GCM y HMAC-SHA256 con claves derivadas por HKDF.
- **Configuración:** `ENCRYPTION_KEY` (obligatoria) y `APP_NAME`.
- **testutil:** `EncryptionKey`, `EnableTwoFactor` y `TwoFactor.Code`.

### Cambiado

- `POST /auth/login` puede responder `202` cuando el usuario tiene 2FA.
- `auth.User` incluye `two_factor_enabled`.
- La configuración quita las comillas que rodean un valor, así un `.env` con `APP_NAME="Mi App"` funciona con make, la shell y systemd.
- sqlc escribe la sigla TOTP en mayúsculas en los nombres generados.

## [v0.2.0] - 2026-10-24

Autenticación completa para proyectos públicos y SaaS, comparable a Laravel Breeze.

### Agregado

- **Perfil:** `PUT /auth/me` para cambiar el nombre.
- **Cambio de contraseña:** `PUT /auth/password` pide la actual, tiene límite de intentos y cierra las demás sesiones (la actual sigue activa).
- **Recuperar la contraseña por correo:**
  - `POST /auth/password/forgot` responde siempre igual y envía un enlace de un solo uso que vence en 1 hora.
  - `POST /auth/password/reset` cambia la contraseña y cierra todas las sesiones.
- **Verificación de email opcional** (`REQUIRE_EMAIL_VERIFICATION`): enlace al registrarse que vence en 24 horas, `POST /auth/email/verify` y `POST /auth/email/resend`. Mientras el email no esté verificado, las rutas protegidas responden `403 email_not_verified`.
- **Paquete `internal/mail`:**
  - envío por SMTP (TLS o STARTTLS);
  - log para desarrollo;
  - envío en segundo plano, que espera los pendientes al apagar;
  - `Recorder` para los tests.
- **Configuración:** `FRONTEND_URL`, `REQUIRE_EMAIL_VERIFICATION`, `MAIL_DRIVER`, `MAIL_FROM` y `SMTP_*`.
- **Plantillas de correo** editables en `internal/auth/emails/`.
- **Tabla `user_tokens`** para tokens de un solo uso (guardados como SHA-256) y columna `users.email_verified_at`.
- **testutil:** `NewAPI` con opciones (`WithMailer`, `WithConfig`) y `TokenFromEmail`.
- **Documentación:** los flujos nuevos, las pantallas que necesita el frontend, Mailpit para desarrollo y cómo quitar cada función.

### Cambiado

- `auth.Register` recibe `auth.Limits` (intentos y correos) en lugar de un solo middleware.
- `auth.User` incluye `email_verified_at`.
- Los admins creados con `create-admin` quedan con el email verificado.

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
