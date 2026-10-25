# Cambios

Historial de versiones de la plantilla. Cada proyecto creado con `gonew` es una copia independiente:
usá esta lista para saber de qué versión salió un proyecto y qué mejoras podrías llevarle a mano.

Formato: una sección por versión, con lo **agregado**, **cambiado** y **corregido**.

La plantilla edita sus migraciones originales, porque representan el esquema inicial. Si tu proyecto ya está
desplegado, no edites las suyas: las versiones con cambios de esquema traen, en **Actualizar un proyecto existente**,
el SQL para ponerlo en una migración nueva (`make migrate-new name=…`).

## [v0.4.0] - 2026-10-24

Trabajos en segundo plano, tiempo real y notificaciones: lo que en Laravel resuelven las colas, el scheduler, Reverb y las notificaciones.

### Agregado

- **Trabajos en segundo plano con River** (`internal/jobs`, ver `docs/jobs.md`), sobre PostgreSQL y sin Redis:
  - los trabajos sobreviven a un reinicio y se reintentan si fallan;
  - trabajos programados y periódicos (el scheduler), únicos por período;
  - encolado dentro de una transacción (`InsertTx`);
  - los workers corren en el mismo binario y se apagan en orden, después del servidor HTTP;
  - el esquema de River entra como migración de goose; `make river-migration` para actualizarlo;
  - `JOBS_MAX_WORKERS`.
- **Trabajo periódico `cleanup`:** cada hora borra las sesiones y los tokens vencidos.
- **Tiempo real con SSE** (`internal/realtime`, ver `docs/realtime.md`):
  - `GET /events` con el canal del usuario y temas con autorización (`?topics=project:42`);
  - `realtime.Stream` para transmitir respuestas largas, como la de un agente de IA;
  - quita el `WriteTimeout` solo para el stream, envía latidos y se cierra al apagar la API;
  - límites de 10 conexiones por usuario y 1000 en total; cada una ocupa unos 11 KB.
- **Notificaciones** (`internal/notifications`): `GET /notifications` (con `?unread` y el contador), `POST /notifications/{id}/read` y `POST /notifications/read-all`. Llegan en vivo por `GET /events`.
- **Avisos de seguridad:** auth notifica cuando se cambia la contraseña y cuando se activa o desactiva el 2FA (`auth.Options.Notifier`, opcional).
- **`notes` publica sus cambios en vivo** (`note.created`, `note.updated`, `note.deleted`), como ejemplo.
- **testutil:** `NewServer`, `OpenEvents`, `WithBroker`; y `auth.WithUser` para probar handlers protegidos.

### Cambiado

- **Los correos pasan por la cola:** se guardan cifrados (llevan enlaces con token), se envían como mucho 4 a la vez y se reintentan hasta 5 veces. Una caída del SMTP o un reinicio de la API ya no pierden correos.
- La limpieza de sesiones y tokens vencidos ya no se hace en cada login.
- sqlc genera las columnas `jsonb` como `json.RawMessage`.

### Quitado

- `mail.Queue`, reemplazada por `jobs.MailSender`.

## [v0.3.8] - 2026-10-24

Últimos detalles de la revisión final.

### Agregado

- **Aviso al arrancar** si en producción `COOKIE_SECURE=false`: las cookies viajan sin HTTPS y sin el prefijo `__Host-`. No es un error, porque un sistema interno puede usar HTTP a propósito. Los avisos de producción se juntan en `config.Config.Warnings`, junto al de `MAIL_DRIVER=log`.

### Corregido

- **El email del login no tenía límite de largo.** Como es la clave del límite por cuenta, cada intento podía ocupar hasta 1 MB en memoria. Ahora se valida el mismo máximo que en el registro (254 caracteres).
- **La cola de correos no tenía tope de envíos simultáneos:** cada correo abría su propia conexión SMTP. Ahora se envían como mucho 4 a la vez.

## [v0.3.7] - 2026-10-24

Solo documentación.

### Cambiado

- **`TRUST_PROXY` con Docker** (README, `.env.example` y `docs/decisions.md`): no publicar el puerto de la API, o publicarlo solo en localhost (`-p 127.0.0.1:8080:8080`). En algunas configuraciones, Docker reenvía las conexiones de afuera desde su gateway privado, y la API volvería a creer un `X-Forwarded-For` inventado.

## [v0.3.6] - 2026-10-24

Protección de `TRUST_PROXY` y una lista de configuración para producción.

### Agregado

- **"Antes de producción"** en el README: lo que hay que revisar en la configuración, sea cual sea el servidor.

### Corregido

- **Con `TRUST_PROXY=true`, se podía inventar la IP:** si el puerto de la API quedaba abierto a internet, quien se conectara directo podía enviar un `X-Forwarded-For` falso y esquivar los límites por IP. Ahora el encabezado solo se cree si la conexión viene de localhost o de una red privada, que es donde está el proxy.
- `X-Forwarded-For` se lee de todos los encabezados, no solo del primero, y se toma el último valor, que es el que agrega el proxy. Si no es una IP válida, se ignora.

## [v0.3.5] - 2026-10-24

Ajustes menores de la revisión de v0.3.4 y alcance de la plantilla.

### Agregado

- **`GOMEMLIMIT`** documentada en el README y en `.env.example`: es una variable del propio Go. En una prueba con 200 logins simultáneos y 12 núcleos, `GOMEMLIMIT=300MiB` bajó el pico de memoria de 590 MiB a 317 MiB, con logins un 10 % más lentos.

### Cambiado

- **`testutil.Login` acepta las mismas opciones que `NewAPI`**, para que la cookie coincida cuando un test usa `CookieSecure`.
- **"Qué no incluye"** (`docs/decisions.md`) deja de presentarse como lista de pendientes: OAuth y el resto se agregan en el proyecto que los necesite. La plantilla no incluye una guía de despliegue, porque depende de cada servidor.

### Corregido

- **La documentación del límite por cuenta** decía que recuperar la contraseña era la salida del dueño. Eso vale cuando el ataque terminó; si sigue, el login de esa cuenta queda bloqueado mientras dure.

## [v0.3.4] - 2026-10-24

Ajustes para producción en un VPS chico, a partir de una revisión con la API levantada.

### Agregado

- **Límite de intentos de login por cuenta:** 10 seguidos por email y luego uno por minuto, vengan de la IP que vengan (`429` con `Retry-After`). Cuenta también los emails que no existen, para no revelar cuáles están registrados. Recuperar la contraseña lo reinicia.
- **Encabezados:** `X-Content-Type-Options: nosniff` en todas las respuestas (`httpx.SecurityHeaders`) y `Cache-Control: no-store` en todo `/auth` (`httpx.NoStore`).
- **Cookies `__Host-` en producción:** con `COOKIE_SECURE=true` se llaman `__Host-session` y `__Host-two_factor_challenge`, así un subdominio no puede crearlas ni pisarlas.

### Cambiado

- `auth.HashPassword` y `auth.VerifyPassword` reciben un `context.Context`.

### Corregido

- **Logins simultáneos podían agotar la memoria:** cada hash de argon2 reserva 19 MiB y no había tope. Con 200 logins simultáneos en 2 núcleos, el proceso pasaba de 16 MiB a 2,5 GiB. Ahora corre como mucho un hash por núcleo: el pico queda en unos 120 MiB, y los logins terminan antes (2,4 s contra 3,3 s).

## [v0.3.3] - 2026-10-24

Dos detalles del 2FA alrededor del cambio de contraseña.

### Corregido

- **Al cambiar o recuperar la contraseña se borran los desafíos del 2FA pendientes.** Antes, quien había pasado el primer paso del login con la contraseña anterior podía completarlo con un código durante los 5 minutos del desafío.
- **Al recuperar la contraseña se reinicia el límite de intentos del 2FA del usuario.** Antes, si alguien con la contraseña anterior lo había agotado, el dueño tenía que esperar hasta una hora para entrar con la nueva.

### Agregado

- `httpx.RateLimiter.Reset`, para olvidar una clave.

## [v0.3.2] - 2026-10-24

Correcciones de seguridad a partir de una revisión del 2FA, del envío de correos y del cifrado.

### Agregado

- **Máximo de 5 intentos por desafío del 2FA:** al agotarlos, el desafío se borra y responde `401 two_factor_locked`; hay que volver a ingresar la contraseña.
- **Límite de intentos del 2FA por usuario:** 10 seguidos y luego uno cada 6 minutos, sin importar desde cuántas IPs lleguen (`429` con `Retry-After`).
- **Aviso por correo** cuando se bloquea un desafío: quien llegó a ese paso conoce la contraseña. Incluye la fecha y la IP. Plantilla en `internal/auth/emails/two_factor_locked.txt`.
- **Límite de correos por destinatario:** "Olvidé mi contraseña", el reenvío de la verificación y el aviso admiten 3 correos seguidos por usuario y luego uno cada 20 minutos.
  - `forgot` sigue respondiendo lo mismo y **no invalida el último enlace enviado**.
  - `resend` responde `429`.
- **`auth.Options.TrustProxy`**, para registrar la IP real en el aviso.
- **testutil:** `DoFromIP`, para simular peticiones desde varias IPs.

### Cambiado

- **El secreto del 2FA se cifra atado a su usuario** (datos asociados de AES-GCM): copiado a otra fila, no se puede descifrar. `secretbox.Encrypt` y `Decrypt` reciben un parámetro `aad`.
- **El HMAC de los códigos de recuperación incluye el id del usuario.**
- Se quitó la lectura del formato cifrado de v0.3.0 (sin identificador de clave).
- **Regla de migraciones** en `AGENTS.md` y `docs/decisions.md`: en un proyecto, hasta el primer despliegue se editan las migraciones originales; después, siempre una nueva.

### Corregido

- **Fuerza bruta del código del 2FA desde muchas IPs:** antes, un código incorrecto no gastaba el desafío y solo había un límite por IP.
- **Correos ilimitados hacia una misma persona** usando varias IPs, que además le anulaban el enlace anterior.
- **El limitador olvidaba una clave inactiva a los 10 minutos** aunque tardara más en recargarse (el de correos tarda 15), y le devolvía todos los intentos. Ahora la recuerda hasta que se recarga.

### Actualizar un proyecto existente

```sql
ALTER TABLE user_tokens ADD COLUMN attempts int NOT NULL DEFAULT 0;
```

Los secretos del 2FA y los códigos de recuperación de versiones anteriores no son compatibles con el formato nuevo. Los usuarios con 2FA tienen que volver a activarlo:

```sql
UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE totp_secret IS NOT NULL;
DELETE FROM user_recovery_codes;
```

## [v0.3.1] - 2026-10-24

Rotación de `ENCRYPTION_KEY` sin afectar a los usuarios, y mejor manejo de errores.

### Agregado

- **`ENCRYPTION_PREVIOUS_KEYS`:** claves anteriores que se siguen aceptando para leer lo cifrado antes de una rotación.
- **Identificador de clave:** cada dato cifrado lo lleva (`52bf1807:…`), así se sabe con qué clave descifrarlo y si hay que volver a cifrarlo.
- **Recifrado gradual:** al usar el 2FA, un secreto cifrado con una clave vieja pasa a la actual.
- **`make rotate-keys` (`./api rotate-keys`):** vuelve a cifrar todo lo que falte y reporta:
  - los secretos que no se pueden descifrar;
  - los usuarios con códigos de recuperación de claves viejas;
  - las claves que ya se pueden quitar.

  Termina con error si la rotación no está completa.
- **`make key` (`./api generate-key`):** genera una clave nueva, sin `openssl`.
- **`docs/encryption.md`:** qué protege la clave, cómo guardarla, la rotación paso a paso, qué hacer si se pierde y la comparación con el `APP_KEY` de Laravel.

### Cambiado

- Los códigos de recuperación guardan el `key_id` de su HMAC y se verifican con todas las claves del llavero.
- `secretbox.New` recibe la clave actual y las anteriores. Hay errores tipados: `ErrUnknownKey` y `ErrDecrypt`.

### Corregido

- Un secreto del 2FA que no se puede descifrar ya no responde `500`: responde `503 two_factor_unavailable` con un mensaje claro, y el log registra la causa y la clave.

### Actualizar un proyecto existente

Los códigos de recuperación guardados no tienen `key_id`. Lo más simple es borrarlos y que cada usuario genere
códigos nuevos desde su perfil; su app de autenticación sigue funcionando:

```sql
DELETE FROM user_recovery_codes;
ALTER TABLE user_recovery_codes ADD COLUMN key_id text NOT NULL;
CREATE INDEX user_recovery_codes_key_id_idx ON user_recovery_codes (key_id);
```

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

### Actualizar un proyecto existente

```sql
ALTER TABLE users
    ADD COLUMN totp_secret     text,
    ADD COLUMN totp_enabled_at timestamptz,
    ADD COLUMN totp_last_step  bigint;

ALTER TABLE user_tokens DROP CONSTRAINT user_tokens_purpose_check;
ALTER TABLE user_tokens ADD CONSTRAINT user_tokens_purpose_check
    CHECK (purpose IN ('password_reset', 'email_verification', 'two_factor_login'));

CREATE TABLE user_recovery_codes (
    code_hash  bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_recovery_codes_user_id_idx ON user_recovery_codes (user_id);
```

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

### Actualizar un proyecto existente

Los usuarios existentes quedan sin verificar. Si vas a activar `REQUIRE_EMAIL_VERIFICATION`, conviene marcarlos
como verificados en la misma migración (`UPDATE users SET email_verified_at = created_at;`).

```sql
ALTER TABLE users ADD COLUMN email_verified_at timestamptz;

CREATE TABLE user_tokens (
    token_hash bytea       PRIMARY KEY,
    user_id    bigint      NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    purpose    text        NOT NULL CHECK (purpose IN ('password_reset', 'email_verification')),
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_tokens_user_id_purpose_idx ON user_tokens (user_id, purpose);
CREATE INDEX user_tokens_expires_at_idx ON user_tokens (expires_at);
```

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
