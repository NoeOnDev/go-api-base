# Cambios

Historial de versiones de la plantilla. Cada proyecto creado con `gonew` es una copia independiente:
usá esta lista para saber qué mejoras podrías llevarle a mano. El archivo `TEMPLATE_VERSION` del proyecto
dice de qué versión salió (o hasta cuál se actualizó): leé las secciones más nuevas que esa.
Al publicar una versión de la plantilla, `TEMPLATE_VERSION` se actualiza en el mismo commit que su sección.

Formato: una sección por versión, con lo **agregado**, **cambiado** y **corregido**.

La plantilla edita sus migraciones originales, porque representan el esquema inicial. Si tu proyecto ya está
desplegado, no edites las suyas: las versiones con cambios de esquema traen, en **Actualizar un proyecto existente**,
el SQL para ponerlo en una migración nueva (`make migrate-new name=…`).

## [v0.5.5] - 2026-10-25

Mejoras de una tercera revisión y documentación sin instrucciones de despliegue. Cambia el esquema: ver **Actualizar un proyecto existente**.

### Agregado

- **`TEMPLATE_VERSION`:** dice de qué versión de la plantilla salió el proyecto. Se copia con el resto; al llevarle cambios de versiones más nuevas, actualizalo a la última que aplicaste.
- **`httpx.Quiet`:** la línea de log de una ruta muy consultada va en `debug`, salvo los 5xx.
- **staticcheck en `make check`** (y suelto con `make lint`), con versión fija como las demás herramientas.

### Cambiado

- **`users.email` exige minúsculas en la base** (`CHECK (email = lower(email))`). La aplicación ya los guardaba así, pero un `INSERT` a mano podía crear `Ana@x.com` junto a `ana@x.com` y saltear el `UNIQUE`.
- **`GET /healthz` se registra en `debug`** si responde bien: un monitor ya no llena el log. Un `503` se sigue registrando como error.
- **La documentación ya no explica cómo desplegar** con Caddy, Nginx, Docker o systemd. Quedan las variables de entorno, los avisos de producción y las reglas de la API, contadas en general.
- `var recorder Recorder`, sin uso en `mail_test.go`, pasa a ser la comprobación de que cada sender cumple `mail.Sender`.

### Actualizar un proyecto existente

```sql
ALTER TABLE users ADD CONSTRAINT users_email_lowercase CHECK (email = lower(email));
```

Si falla, hay emails con mayúsculas guardados a mano. Antes de pasarlos a minúsculas, revisá que no queden dos cuentas con el mismo email:

```sql
SELECT lower(email), count(*) FROM users GROUP BY lower(email) HAVING count(*) > 1;
UPDATE users SET email = lower(email) WHERE email <> lower(email);
```

## [v0.5.4] - 2026-10-25

Cambios de documentación: la presentación de la plantilla. Sin cambios de código ni de esquema.

### Cambiado

- **La plantilla ya no se presenta como "liviana" ni para proyectos chicos:** el README y `AGENTS.md` la describen como una base completa y lista para producción, para proyectos personales, de clientes y de empresa. Siguen siendo pocas dependencias, un solo binario y una instancia; el README lo dice en la presentación.
- `docs/decisions.md`: la lista de dependencias del binario incluye River, y "Librería estándar primero" explica que lo que crece es el código propio, no las dependencias. El servicio de claves externo y los parámetros de argon2 ya no se justifican por "un VPS chico".

## [v0.5.3] - 2026-10-25

Correcciones de otra revisión. Sin cambios de esquema: no hace falta SQL de actualización (cambian consultas, así que hay que correr `make sqlc`).

### Corregido

- **Activar el 2FA podía dejar activo un secreto que el usuario nunca escaneó.** `enable` validaba el código con el secreto pendiente y después activaba cualquier secreto. Si entre los dos pasos otra petición pedía un QR nuevo (`setup`, que solo pide sesión), quedaba activo ese secreto, y quien hizo el `setup` lo recibía en la respuesta. Ahora `EnableTOTP` exige que el secreto sea el verificado; si cambió, responde `409`. Para que una rotación de claves entre `setup` y `enable` no rompa la comparación, `enable` ya no vuelve a cifrar el secreto pendiente: lo pasan a la clave actual el login o `rotate-keys`.
- **Iniciar sesión dejaba viva la sesión anterior del mismo navegador.** Con un login sin logout o un cambio de cuenta, la cookie nueva reemplazaba a la vieja, pero la fila seguía hasta vencer y aparecía en `GET /auth/sessions` como otro dispositivo. Ahora `StartSession` (login, registro y segundo paso del 2FA) cierra la sesión de la cookie de la petición y corta sus streams. Las sesiones de otros dispositivos no se tocan.

### Agregado

- **Avisos de producción para `FRONTEND_URL` y `MAIL_FROM`:** si siguen en `localhost`, la API lo avisa en el log al arrancar, como ya hacía con `COOKIE_SECURE` y `MAIL_DRIVER`. Con `FRONTEND_URL` en localhost, los enlaces de los correos no funcionan; con `MAIL_FROM`, los proveedores rechazan los correos.
- **Rehash de las contraseñas al entrar:** si los parámetros de argon2 cambian, cada contraseña se vuelve a hashear con los nuevos la próxima vez que su usuario inicia sesión. Antes se quedaban con los parámetros viejos para siempre.

### Cambiado

- `server.Deps.Box`: el llavero de `ENCRYPTION_KEY` se pasa en `Deps`, en lugar de que `NewHandler` arme el suyo desde `Config`. `main` pasa el mismo que usan los jobs; sin él, el 2FA no está disponible. `testutil.NewAPI` lo arma solo.
- `EnableTOTP` recibe el secreto verificado (`store.EnableTOTPParams.TOTPSecret`).
- Los comentarios del apagado en `cmd/api/main.go` describen el código actual, y algunos que contaban cómo era antes quedan en presente.

## [v0.5.2] - 2026-10-25

Correcciones de otra revisión del proyecto completo. Sin cambios de esquema: no hace falta SQL de actualización (cambia una consulta, así que hay que correr `make sqlc`).

### Corregido

- **Con dos proxies, la IP del cliente era la del primero.** Con un CDN delante del proxy (Cloudflare → Caddy → API), `httpx.ClientIP` tomaba el último valor de `X-Forwarded-For`, que es la IP de Cloudflare: todos los usuarios de un mismo nodo compartían los límites por IP y recibían `429`, y las sesiones mostraban esa IP. Ahora se recorre de derecha a izquierda salteando los proxies de `TRUSTED_PROXIES`, como `trusted_proxies_strict` de Caddy. Para Cloudflare, sumá sus rangos a `TRUSTED_PROXIES` y configurá `trusted_proxies` en Caddy (ver "Antes de producción" en el README). Con un solo proxy, nada cambia.
- **Activar o desactivar el 2FA podía quedar a medias.** Si fallaba guardar los códigos de recuperación o cerrar las demás sesiones, el 2FA quedaba activo sin códigos y con las otras sesiones abiertas, y el reintento respondía `409`. Ahora los pasos van en una transacción.
- **`GET /auth/sessions` mostraba sesiones vencidas por inactividad** hasta que la limpieza periódica las borraba. Ahora la lista trae solo las que todavía sirven.

### Agregado

- **`make rotate-keys` cuenta los correos de la cola** cifrados con claves anteriores. Antes, el reporte podía indicar que una clave se podía quitar mientras quedaban correos cifrados con ella, que se cancelaban al quitarla; había que revisarlo a mano con un `SELECT`. Ahora, mientras queden, la clave no figura como removible y el comando termina con error. Lo hacen `jobs.QueuedEmailKeys` y `auth.KeyReport.AddQueuedEmails`.
- `docs/decisions.md`: qué revela la API sobre los emails registrados (el login y la recuperación no; el registro y el cambio de email sí, con `409`) y cómo ocultarlo del todo si un proyecto lo necesita.
- `SMTP_TEST_ADDR` documentada en `AGENTS.md` y `.env.example`: con Mailpit (`localhost:1025`), los tests prueban también el envío real por SMTP.

### Cambiado

- **`?offset=` acepta como mucho 10000** (`httpx.MaxOffset`); uno mayor responde `422`. Un offset enorme obligaba a PostgreSQL a leer y descartar todas las filas anteriores. Para recorrer más, conviene paginar por cursor (ver el README).
- `github.com/riverqueue/river/rivertype` pasa a ser dependencia directa en `go.mod`: es la misma versión que ya estaba como indirecta.

## [v0.5.1] - 2026-10-25

Correcciones de una revisión del proyecto completo. Sin cambios de esquema: no hace falta SQL de actualización.

### Corregido

- **Activar el 2FA no pedía la contraseña.** Alguien con una sesión robada podía activarlo con su propia app: como `enable` cierra las demás sesiones y recuperar la contraseña no desactiva el 2FA, el dueño quedaba afuera hasta que un admin lo desactivara a mano. Ahora `POST /auth/two-factor/enable` pide `{code, password}`, como desactivarlo.
- **Confirmar un email nuevo no anulaba los enlaces de recuperación enviados al anterior.** Siguieron sirviendo hasta una hora: si el usuario cambiaba el email porque había perdido el correo viejo, quien lo tuviera podía recuperar la contraseña y quedarse con la cuenta. Ahora se borran al confirmar.
- **Cambiar o recuperar la contraseña no cancelaba un cambio de email pendiente.** Quien conocía la contraseña anterior podía pedir el cambio a un correo suyo y confirmarlo después de que el dueño la cambiara, para quedarse con el email y, desde ahí, con la cuenta. Ahora `savePassword` borra también ese enlace.

### Cambiado

- **`POST /auth/two-factor/enable` recibe `{code, password}`.** El frontend tiene que pedir la contraseña en la pantalla de activación.
- **El pool de PostgreSQL tiene 10 conexiones por defecto**, en lugar de las 4 de pgx (o una por núcleo), si `DATABASE_URL` no indica `pool_max_conns`. River ocupa una conexión fija para LISTEN y comparte el resto con la API.
- `testutil.EnableTwoFactor(t, api, cookie, password)` recibe la contraseña del usuario. Para uno creado con `testutil.Login` es `testutil.Password`.

## [v0.5.0] - 2026-10-24

Funciones de cuenta: las que Laravel Jetstream suma sobre Breeze.

### Agregado

- **Sesiones por dispositivo:** cada sesión guarda el navegador, la IP y su última actividad (como mucho cada 5 minutos).
  - `GET /auth/sessions` las lista y marca la actual;
  - `DELETE /auth/sessions/{id}` cierra una y corta sus streams;
  - `POST /auth/sessions/revoke-others` con la contraseña cierra todas las demás.
- **Vencimiento por inactividad (`SESSION_IDLE_TIMEOUT`)**, opcional: una sesión sin actividad durante ese tiempo deja de servir. Un stream SSE abierto no cuenta como actividad, y la limpieza periódica borra las inactivas.
- **Cambiar el email:** `PUT /auth/email` con la contraseña envía un enlace al email nuevo y un aviso al actual; `POST /auth/email/confirm` lo cambia y lo deja verificado. Notificación `email_changed`.
- **Borrar la cuenta:** `DELETE /auth/me` con la contraseña borra al usuario y todos sus datos, corta sus streams y le envía un último correo. No deja borrar al único admin.
- Plantillas de correo `email_change.txt`, `email_change_notice.txt` y `account_deleted.txt`.

### Cambiado

- **`auth.Service.StartSession(w, r, userID)`** recibe la petición, para guardar el navegador y la IP.
- **Migraciones editadas:** `00002_create_sessions.sql` (`id`, `user_agent`, `ip`, `last_seen_at`) y `00006_create_user_tokens.sql` (`email` y el propósito `email_change`). Como la plantilla todavía no tiene proyectos derivados, no hay SQL de actualización: se recrea la base.
- "Qué no incluye" ya no lista cambiar el email.

## [v0.4.6] - 2026-10-24

Correcciones de otra revisión del proyecto completo.

### Corregido

- **El límite por IP de `/auth` era uno solo para todas las rutas** (5 seguidos y uno cada 12 s): detrás de una IP compartida (una oficina, una universidad, CGNAT), los usuarios legítimos recibían `429`, y un login con 2FA gastaba dos intentos. Ahora cada grupo tiene su límite (login, registro, 2FA, tokens y correos), más generoso, y las acciones con sesión (cambiar la contraseña, el 2FA) se limitan por usuario. Los valores quedan juntos en `internal/server/routes.go`.
- **`Recover` agregaba el JSON del error a una respuesta ya empezada**, por ejemplo a mitad de un stream SSE. Ahora registra el panic y corta la conexión.

### Agregado

- **`TRUSTED_PROXIES`:** las IPs o rangos CIDR exactos del proxy. Sin definirla, se sigue confiando en localhost y en toda red privada. Algunos proveedores tienen redes privadas compartidas entre clientes.
- `docs/adding-a-resource.md`: `pg_trgm` para las búsquedas con `ILIKE` cuando la tabla crece.

### Cambiado

- `httpx.NewRateLimiter(every, burst)` ya no recibe los proxies: el middleware elige la clave (`MiddlewareByIP(proxies)` o `MiddlewareBy(clave)`).
- `httpx.ClientIP(r, proxies)` y `auth.Options.TrustedProxies` reciben la lista de proxies de confianza, en lugar de un booleano.
- `auth.Limits` tiene un campo por grupo de rutas.

## [v0.4.5] - 2026-10-24

Correcciones de una revisión nueva del proyecto completo.

### Corregido

- **Un cliente SSE que dejaba de leer trababa su stream.** `Open` quitaba el `WriteTimeout` y no ponía otro plazo: la escritura se bloqueaba con los buffers de TCP llenos, el handler no veía ni el logout ni el apagado, y la conexión seguía contando para los límites. Ahora cada escritura tiene un plazo de 10 segundos.
- **Si el apagado se pasaba del tiempo, River no se detenía.** `run` salía antes de detener los trabajos, y uno cortado se reintentaba recién a la hora. Ahora el servidor HTTP y River se detienen en paralelo, cada uno con el timeout completo, y las peticiones que quedan se cortan.
- **Recuperar la contraseña no era atómico:** el enlace se gastaba antes de guardar la contraseña. Ahora consumir el token, guardar la contraseña, marcar el email y cerrar las sesiones van en una transacción. Lo mismo al cambiarla desde el perfil.
- **Dos pedidos simultáneos de enlace dejaban dos enlaces válidos.** `issueToken` ahora va en una transacción que bloquea la fila del usuario.
- **Si no se podía encolar el correo de verificación, el registro respondía `500`** aunque la cuenta ya existía (y un reintento daba `409`). Ahora responde `201`, y el usuario puede pedir otro enlace.
- **`httpx.Write` enviaba el status antes de codificar el JSON:** si la codificación fallaba, el cliente recibía un `200` con el cuerpo cortado y el error quedaba en el log sin el `request_id`. Ahora codifica antes y, si falla, `Recover` responde `500` y lo registra con el `request_id`.

### Cambiado

- **`auth.NewService` recibe el pool** (`*pgxpool.Pool`) en lugar de las consultas, para poder abrir transacciones.
- **Sin `TEST_DATABASE_URL`, los tests de integración fallan en CI** (`CI=true`) en lugar de saltearse en silencio, y `make test` lo avisa al empezar.
- `docs/adding-a-resource.md` muestra el patrón de transacción con `pgx.BeginFunc`, y `docs/decisions.md` explica el apagado con Docker y systemd.

## [v0.4.4] - 2026-10-24

Solo documentación.

### Corregido

- **El ejemplo de `handleClosed` en `docs/realtime.md`** mostraba el aviso de verificar el email (y no reintentaba) a cualquier usuario sin verificar, aunque la verificación no fuera obligatoria y el corte fuera por un `429` o un `503`. Ahora pregunta a `/notifications`, que pasa por los mismos controles que `/events`: el `403 email_not_verified` solo aparece cuando la verificación es obligatoria.

## [v0.4.3] - 2026-10-24

Solo documentación.

### Corregido

- **El ejemplo de frontend de `docs/realtime.md`:**
  - la conexión se arma en una función `connect()`, así un reintento crea el `EventSource` nuevo con sus listeners (antes `onerror` llamaba a una `connect` que no existía);
  - con un email sin verificar no reintenta para siempre: `GET /events` responde `403 email_not_verified`, y el ejemplo lo detecta con `/auth/me`. Además explica que la conexión se abre recién cuando el email está verificado.

## [v0.4.2] - 2026-10-24

Ajustes de la revisión de v0.4.1.

### Cambiado

- **Un tema sin permiso ya no rechaza toda la conexión de `GET /events` con `403`.** Se informa en el evento `ready` (`{"topics": […], "rejected": […]}`) y el usuario sigue recibiendo su canal y los demás temas. Antes, a alguien a quien sacaban de un proyecto se le cortaba también el canal de sus notificaciones, y el frontend no podía saber qué tema había perdido.
- **La vida de un stream varía al azar entre 13 y 17 minutos**, en lugar de 15 fijos, para que después de un reinicio no se renueven todas las pestañas a la vez.

### Corregido

- **El ejemplo de `onerror` en `docs/realtime.md`** mandaba al login con cualquier conexión cerrada, aunque fuera por demasiadas conexiones. Ahora pregunta a `GET /auth/me`: con `401` va al login; si no, reintenta más tarde.

## [v0.4.1] - 2026-10-24

Correcciones de la revisión de v0.4.0.

### Corregido

- **Un stream de `GET /events` sobrevivía a su sesión.** La sesión se revisaba solo al conectarse, y el latido mantenía viva la conexión: alguien con una sesión robada seguía recibiendo eventos después de que el dueño recuperaba la contraseña, incluido el aviso `password_changed`. Ahora:
  - se corta en el acto con el logout de esa sesión, y cuando auth cierra las demás sesiones del usuario (cambio o recuperación de contraseña, 2FA), antes del aviso de seguridad;
  - se revisa la sesión con cada latido (cada 25 segundos);
  - dura como mucho 15 minutos: al reconectarse, se vuelven a revisar la sesión y los permisos de los temas.

### Agregado

- `auth.Options.SessionWatcher` (lo implementa el broker), `auth.Service.SessionKey` y `CheckSession`.
- `broker.DisconnectUser`, para quitarle a alguien el acceso a un tema al instante.
- La limpieza periódica borra las notificaciones leídas hace más de 90 días.
- `docs/encryption.md`: antes de quitar una clave anterior, confirmar que no queden correos en la cola.

### Cambiado

- `realtime.Broker.Handler` recibe la interfaz `realtime.Sessions` (en la plantilla, el servicio de auth).

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
