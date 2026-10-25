# Autenticación

La API usa **sesiones guardadas en PostgreSQL** y enviadas en una **cookie `HttpOnly`**. No usa JWT.

Incluye:
- registro, login y logout;
- perfil y cambio de contraseña;
- recuperar la contraseña por correo;
- verificar el email (opcional);
- 2FA con app de autenticación y códigos de recuperación (voluntario para cada usuario);
- roles `user`/`admin`.

Cada función se puede quitar si un proyecto no la necesita (ver [al final](#quitar-funciones)).

## Cómo funcionan las sesiones

1. **Inicio de sesión:** `POST /auth/register` o `POST /auth/login` crean una sesión. La API genera un token aleatorio de 256 bits, guarda en la base solo su SHA-256 y envía el token en la cookie `session`.
2. **Cada petición:** el navegador envía la cookie automáticamente. `RequireUser` busca la sesión, comprueba que no haya vencido (`SESSION_TTL`, 7 días por defecto) y deja el usuario en el contexto.
3. **Cierre de sesión:** `POST /auth/logout` borra la sesión de la base y la cookie del navegador.

La cookie tiene estos atributos:

| Atributo | Para qué |
|---|---|
| `HttpOnly` | JavaScript no puede leerla: un ataque XSS no puede robar la sesión |
| `SameSite=Lax` | El navegador no la envía en peticiones disparadas por otro sitio |
| `Secure` | Solo viaja por HTTPS. Se controla con `COOKIE_SECURE` y está activado por defecto en producción |
| `Path=/` | Vale para toda la API |
| Sin `Domain` | Solo vale para el host que la creó, no para sus subdominios |
| Prefijo `__Host-` | Solo en producción (`COOKIE_SECURE=true`): la cookie se llama `__Host-session`, y el navegador la rechaza si no es `Secure`, con `Path=/` y sin `Domain`. Así ningún subdominio puede crearla ni pisarla. En desarrollo se llama `session` |

El desafío del 2FA usa una cookie con los mismos atributos: `two_factor_challenge`, o `__Host-two_factor_challenge` en producción. El frontend no necesita conocer estos nombres, porque las cookies son `HttpOnly` y viajan solas.

## Endpoints

| Método | Ruta | Acceso | Cuerpo | Respuesta |
|---|---|---|---|---|
| `POST` | `/auth/register` | público | `{name, email, password}` | `201` con el usuario y la cookie; `409` si el email ya existe; `422` |
| `POST` | `/auth/login` | público | `{email, password}` | `200` con el usuario y la cookie; `202 {two_factor_required: true}` si tiene 2FA; `401` si los datos no coinciden |
| `POST` | `/auth/logout` | público | | `204`, siempre (también si la sesión ya venció) |
| `GET` | `/auth/me` | sesión | | `200` con el usuario |
| `PUT` | `/auth/me` | sesión | `{name}` | `200` con el usuario |
| `DELETE` | `/auth/me` | sesión | `{password}` | `204`, borra la cuenta; `422` si la contraseña es incorrecta; `409` si es el único admin |
| `PUT` | `/auth/email` | sesión | `{email, password}` | `202`, envía el enlace al email nuevo; `409` si está en uso; `422` |
| `POST` | `/auth/email/confirm` | público | `{token}` | `204`, cambia el email; `422` si el enlace no es válido o venció; `409` si el email lo tomó otra cuenta |
| `GET` | `/auth/sessions` | sesión | | `200 {data: [{id, user_agent, ip, created_at, last_seen_at, current}]}` |
| `DELETE` | `/auth/sessions/{id}` | sesión | | `204`; `404` si no es del usuario; `422` si es la actual |
| `POST` | `/auth/sessions/revoke-others` | sesión | `{password}` | `204`, cierra todas las demás |
| `PUT` | `/auth/password` | usuario | `{current_password, password}` | `204`; `422` si la contraseña actual es incorrecta |
| `POST` | `/auth/password/forgot` | público | `{email}` | `202`, siempre |
| `POST` | `/auth/password/reset` | público | `{token, password}` | `204`; `422` si el enlace no es válido o venció |
| `POST` | `/auth/email/verify` | público | `{token}` | `204`; `422` si el enlace no es válido o venció |
| `POST` | `/auth/email/resend` | sesión | | `202`; `204` si el email ya estaba verificado |
| `POST` | `/auth/two-factor/challenge` | cookie del desafío | `{code}` o `{recovery_code}` | `200` con el usuario y la cookie de sesión; `422` si el código es incorrecto; `401` si el desafío venció; `503` si el secreto no se puede descifrar |
| `POST` | `/auth/two-factor/setup` | usuario | | `200 {secret, otpauth_url}`; `409` si ya está activo |
| `POST` | `/auth/two-factor/enable` | usuario | `{code}` | `200 {recovery_codes}`; `422` si el código es incorrecto |
| `POST` | `/auth/two-factor/disable` | usuario | `{password}` | `204` |
| `POST` | `/auth/two-factor/recovery-codes` | usuario | `{password}` | `200 {recovery_codes}` nuevos |

En la columna de acceso:
- **sesión:** cualquier usuario logueado, aunque no haya verificado su email.
- **usuario:** además exige el email verificado, si `REQUIRE_EMAIL_VERIFICATION=true`.

El usuario se devuelve así (nunca incluye la contraseña):

```json
{"id": 1, "name": "Ana", "email": "ana@ejemplo.com", "role": "user", "email_verified_at": null, "two_factor_enabled": false, "created_at": "..."}
```

**Reglas:**
- Nombre obligatorio, de hasta 100 caracteres.
- Email válido, que se guarda en minúsculas.
- Contraseña de 8 a 128 caracteres.

## Perfil y cambio de contraseña

- **`PUT /auth/me`** cambia el nombre. El email se cambia aparte (ver "Cambiar el email"), porque hay que verificar la dirección nueva.
- **`PUT /auth/password`** pide la contraseña actual, así alguien que encuentre la sesión abierta no puede cambiarla. Al cambiarla se **cierran todas las demás sesiones** del usuario y la actual sigue activa.

## Sesiones por dispositivo

Cada sesión guarda el navegador (user-agent), la IP con la que se inició y su última actividad, que se actualiza como mucho cada 5 minutos para no escribir en la base en cada petición.

- **`GET /auth/sessions`** las lista, de la más reciente a la más vieja, y marca la de este dispositivo (`current`). El user-agent llega tal cual: para mostrarlo legible ("Firefox en Windows"), el frontend puede usar una librería como `ua-parser-js`.
- **`DELETE /auth/sessions/{id}`** cierra una, por ejemplo la de un dispositivo que el usuario no reconoce. Se cortan en el acto sus streams de `GET /events`. La actual se cierra con `POST /auth/logout`.
- **`POST /auth/sessions/revoke-others`** con `{password}` cierra todas las demás. Pide la contraseña, como Laravel Jetstream, porque es masivo: así alguien con una sesión robada no puede echar al dueño de todos sus dispositivos.

**Vencimiento por inactividad (opcional):** con `SESSION_IDLE_TIMEOUT` (por ejemplo `30m`), una sesión sin actividad durante ese tiempo deja de servir, además del vencimiento fijo de `SESSION_TTL`. Tiene que ser de al menos 15 minutos, porque la actividad se guarda cada 5. Un stream de `GET /events` abierto no cuenta como actividad: una pestaña olvidada no mantiene viva la sesión.

## Cambiar el email

1. El frontend llama a **`PUT /auth/email`** con `{email, password}`.
   - **Pide la contraseña:** si no, alguien con una sesión robada cambiaría el email, después recuperaría la contraseña con él y se quedaría con la cuenta.
   - Si el email ya está en uso responde `409`, igual que el registro.
2. La API envía un enlace a **`FRONTEND_URL/confirm-email?token=…`** a la dirección **nueva**, que vence en 1 hora, y un **aviso a la actual**, por si el pedido no lo hizo el dueño.
3. **El email no cambia todavía.** La pantalla `/confirm-email` del frontend toma el `token` y llama a **`POST /auth/email/confirm`** con `{token}`. Recién ahí cambia, y queda verificado. Es pública, como la verificación, así funciona aunque el enlace se abra en otro dispositivo.

Al confirmar se envía la notificación `email_changed`. Si mientras tanto otra cuenta tomó ese email, responde `409` y no cambia nada.

## Borrar la cuenta

**`DELETE /auth/me`** con `{password}` borra la cuenta y todos sus datos: sesiones, tokens, códigos de recuperación, notas y notificaciones, por `ON DELETE CASCADE`. Se cortan sus streams, se borra la cookie y se le envía un último correo ("tu cuenta fue eliminada"), por si no fue el dueño.

- **No se puede borrar la única cuenta de administrador** (`409`): primero hay que nombrar otro admin. Así nadie deja la API sin administrador por error.
- **Cada tabla de un proyecto con datos del usuario tiene que referenciarlo con `ON DELETE CASCADE`** (o `SET NULL`, si los datos deben quedar sin dueño, como una factura). Con `RESTRICT`, el borrado falla con `409` y no se borra nada.
- Los correos que ya estaban en la cola para ese usuario se envían igual.

## Recuperar la contraseña

1. El usuario pide el enlace: `POST /auth/password/forgot` con su email.
   - La API responde **siempre** `202` con el mismo mensaje, exista o no el email, para no revelar qué cuentas existen.
   - Si existe, envía un correo con un enlace a **`FRONTEND_URL/reset-password?token=…`**, que vence en 1 hora y sirve una sola vez.
2. La pantalla `/reset-password` del frontend toma el `token` de la URL, pide la contraseña nueva y llama a `POST /auth/password/reset` con `{token, password}`.
3. La API cambia la contraseña, **cierra todas las sesiones** del usuario y marca su email como verificado (demostró que recibe correos en esa dirección). El usuario vuelve a iniciar sesión.

Pedir un enlace nuevo invalida el anterior. Si la contraseña no cumple las reglas, el token no se gasta y el usuario puede corregirla.

## Verificar el email

Con **`REQUIRE_EMAIL_VERIFICATION=true`**:

1. Al registrarse, el usuario recibe un correo con un enlace a **`FRONTEND_URL/verify-email?token=…`**, que vence en 24 horas.
2. Hasta que lo abra, las rutas protegidas (`RequireUser`, `RequireAdmin`) responden **`403` con el código `email_not_verified`**. El frontend debería mostrar un aviso con un botón para reenviar el correo (`POST /auth/email/resend`).
3. La pantalla `/verify-email` del frontend toma el `token` y llama a `POST /auth/email/verify` con `{token}`.

Mientras no verificó, el usuario sí puede usar `/auth/me` (que trae `email_verified_at: null`), editar su perfil, cerrar sesión y pedir otro enlace. Los admins creados con `create-admin` quedan verificados.

Con la opción en `false` (por defecto) no se envía ningún correo al registrarse y nada se bloquea.

## 2FA (verificación en dos pasos)

Cada usuario puede activarlo si quiere. Funciona con cualquier app de autenticación: Google Authenticator, Microsoft Authenticator, Authy, 1Password, Bitwarden, etc.

### Activar

1. El frontend llama a `POST /auth/two-factor/setup` y recibe `{secret, otpauth_url}`.
2. Muestra `otpauth_url` como **código QR** (en React, con `qrcode.react`: `<QRCodeSVG value={otpauth_url} />`) y `secret` en texto, para cargarlo a mano.
3. El usuario lo escanea con su app y escribe el código de 6 dígitos que aparece.
4. El frontend llama a `POST /auth/two-factor/enable` con `{code}`. Si es correcto, el 2FA queda activo y la respuesta trae **10 códigos de recuperación**.
5. El frontend muestra los códigos **una sola vez**, con la opción de copiarlos o descargarlos. Sirven para entrar si el usuario pierde el teléfono.

Hasta que se confirma con `enable`, el 2FA no está activo, así que un QR mal escaneado no deja a nadie bloqueado. Al activarlo se cierran las demás sesiones.

### Iniciar sesión con 2FA

1. `POST /auth/login` con email y contraseña correctos responde **`202 {"two_factor_required": true}`** y una cookie `HttpOnly` con el desafío, que vence en 5 minutos. Todavía no hay sesión.
2. El frontend muestra el campo del código y llama a `POST /auth/two-factor/challenge` con:
   - `{"code": "123456"}`, el código de la app; o
   - `{"recovery_code": "xxxxx-xxxxx"}`, un código de recuperación (se acepta en mayúsculas o sin guion).
3. Si es correcto, responde `200` con el usuario y crea la sesión, como un login normal.

Un código incorrecto responde `422` y deja reintentar. Cada desafío admite **5 intentos**:
- Al quinto código incorrecto responde **`401 two_factor_locked`**, borra el desafío y le envía al usuario un **correo de aviso** con la fecha y la IP: quien llegó a ese paso conoce su contraseña.
- Si el desafío venció o ya no existe, responde `401` y hay que volver a ingresar la contraseña.
- Además, cada usuario tiene **10 intentos seguidos y luego uno cada 6 minutos**, vengan de la IP que vengan. Al superarlos responde `429` con `Retry-After`.

### Desactivar y regenerar códigos

- **`POST /auth/two-factor/disable`** con `{password}`: desactiva el 2FA, borra los códigos de recuperación y cierra las demás sesiones.
- **`POST /auth/two-factor/recovery-codes`** con `{password}`: genera 10 códigos nuevos; los anteriores dejan de servir.

### Seguridad del 2FA

- **Códigos TOTP estándar** (RFC 6238): 6 dígitos, cambian cada 30 segundos y se tolera ±30 segundos de desfase del reloj.
- **Un código ya usado se rechaza** aunque siga vigente, así que un código interceptado no sirve para otro login.
- **El secreto se guarda cifrado** con AES-256-GCM y la clave `ENCRYPTION_KEY`, atado al usuario. Los códigos de recuperación se guardan como HMAC, nunca en texto plano, y cada uno sirve una vez.
- **Fuerza bruta del código:** 5 intentos por desafío y un límite por usuario, independiente de la IP (ver arriba). Con eso, adivinar un código queda cerca de un 0,07 % por día.
- **Recuperar la contraseña no saltea el 2FA:** el siguiente login vuelve a pedir el código.
- **Si un usuario pierde el teléfono y los códigos**, un admin puede desactivarle el 2FA desde la base:

  ```sql
  UPDATE users SET totp_secret = NULL, totp_enabled_at = NULL, totp_last_step = NULL WHERE email = '…';
  ```

**Cuidado con `ENCRYPTION_KEY`:** es obligatoria y hay que guardarla como cualquier secreto de producción. Se puede rotar sin afectar a nadie, y si se pierde, el segundo paso del login responde `503 two_factor_unavailable` con un mensaje claro. Todo esto está en [docs/encryption.md](encryption.md).

## Correos

| Variable | Para qué |
|---|---|
| `MAIL_DRIVER` | `log`: escribe los correos en el log, para desarrollo sin servidor. `smtp`: los envía |
| `MAIL_FROM` | Remitente |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | Datos SMTP de tu proveedor (Resend, Brevo, Amazon SES, Mailgun o el correo del hosting), normalmente con el puerto 587 |
| `FRONTEND_URL` | Base de los enlaces de los correos |

**Cómo se envían:**
- **Por la cola de trabajos** (`jobs.MailSender`, ver [docs/jobs.md](jobs.md)): la respuesta HTTP no espera al servidor de correo, y el correo se guarda cifrado en la base hasta que sale.
- **Si un envío falla**, se reintenta hasta 5 veces, con una espera cada vez más larga, y cada error queda en el log. Una caída del SMTP o un reinicio de la API no pierden correos.
- **Conexión cifrada:** con el puerto 465 se usa TLS desde el inicio; con los demás, STARTTLS, que es obligatorio salvo contra `localhost`.

**En desarrollo** se recomienda **Mailpit**, un servidor SMTP falso que muestra los correos en `http://localhost:8025`:

```env
MAIL_DRIVER=smtp
SMTP_HOST=localhost
SMTP_PORT=1025
```

Las plantillas están en `internal/auth/emails/*.txt`, en texto plano. Para cambiar los textos, editá esos archivos.

## Roles

- **`user`:** el rol de quien se registra con `/auth/register`.
- **`admin`:** se crea con el comando `create-admin`:

  ```bash
  make create-admin email=admin@cliente.com name="Administrador"
  ```

  En producción, con el binario:

  ```bash
  ./api create-admin -email admin@cliente.com
  ```

  La contraseña se pide por la terminal sin mostrarse, o se toma de la variable `ADMIN_PASSWORD`.

En las rutas, `RequireAdmin` responde `401` si no hay sesión y `403` si el usuario no es admin.

## Seguridad incluida

- **Contraseñas con argon2id** (19 MiB, 2 iteraciones, 1 hilo, según OWASP), con sal aleatoria.
- **Login sin filtraciones:** siempre responde el mismo `401` y tarda lo mismo exista o no el email. "Olvidé mi contraseña" también responde siempre lo mismo. El registro sí indica si un email está en uso (`409`), porque si no el usuario no sabría por qué falla.
- **Tokens de un solo uso** para recuperar la contraseña y verificar el email: en la base solo se guarda su SHA-256, vencen, y se validan y borran en una sola operación, así que no se pueden usar dos veces.
- **Las sesiones se cierran al cambiar o recuperar la contraseña**, por si alguien había entrado con la anterior.
- **Una contraseña nueva corta los logins con 2FA a medio hacer:** al cambiarla o recuperarla se borran los desafíos pendientes, que se obtuvieron con la anterior. Al recuperarla, además, se reinicia el límite de intentos del 2FA del usuario, por si alguien con la contraseña vieja lo había agotado.
- **Límites de peticiones por grupo de rutas**, cada uno por separado (el segundo paso del 2FA no gasta los intentos del login). Al superarlos se responde `429` con `Retry-After`. Los valores están juntos en `internal/server/routes.go`, para ajustarlos:

  | Grupo | Rutas | Por | Límite |
  |---|---|---|---|
  | Login | `POST /auth/login` | IP | 20 seguidos, luego 1 cada 3 s |
  | Registro | `POST /auth/register` | IP | 10 seguidos, luego 1 cada 10 s |
  | 2FA | `POST /auth/two-factor/challenge` | IP | 20 seguidos, luego 1 cada 3 s |
  | Tokens | `reset`, `email/verify` | IP | 10 seguidos, luego 1 cada 6 s |
  | Correos | `forgot`, `resend` | IP | 10 seguidos, luego 1 por minuto |
  | Cuenta | cambiar la contraseña y administrar el 2FA | **usuario** | 5 seguidos, luego 1 cada 12 s |

  Los de IP son generosos porque muchas personas pueden compartir una IP (una oficina, una universidad, los datos móviles). Las IPv6 se cuentan por bloque `/64`. Los de cuenta van por usuario, así los usuarios detrás de una misma IP no se cruzan.
- **Límites por usuario**, aunque los pedidos lleguen de muchas IPs:
  - **Login:** 10 intentos seguidos por email y luego uno por minuto. Cuenta también los emails que no existen, para no revelar cuáles están registrados. Recuperar la contraseña lo reinicia, aunque contra un ataque que sigue en curso eso no alcanza (ver [decisiones](decisions.md#límite-de-peticiones-en-memoria)).
  - **Código del 2FA:** 10 intentos seguidos y luego uno cada 6 minutos.
  - **Correos a una misma persona:** 3 seguidos de cada tipo (recuperación, verificación, aviso) y luego uno cada 20 minutos. En `forgot`, al superarlo no se envía nada y la respuesta es la misma de siempre, para no revelar si el email existe; el último enlace enviado sigue sirviendo. `resend` responde `429`.
- Los límites están en memoria: alcanzan para una instancia de la API, y se reinician con ella.
- **Como mucho un hash de argon2 por núcleo a la vez**, para que muchos logins simultáneos no agoten la memoria del servidor. Los demás esperan su turno.
- **Encabezados:** `X-Content-Type-Options: nosniff` en todas las respuestas y `Cache-Control: no-store` en todas las de `/auth`.
- **2FA** opcional para cada usuario (ver la sección [2FA](#2fa-verificación-en-dos-pasos)).
- **Protección CSRF** (`http.CrossOriginProtection` de Go): rechaza con `403` los `POST`/`PUT`/`DELETE` que el navegador envía desde otro sitio. Las peticiones sin encabezados de navegador (`curl`, apps móviles) no se ven afectadas.
- **Limpieza:** las sesiones y los tokens vencidos se borran al crear nuevos.

## Desde el frontend (React)

**Pantallas que necesita el frontend:**
- login y registro;
- perfil (nombre y cambio de contraseña);
- "olvidé mi contraseña";
- `/reset-password`, la del enlace del correo;
- `/verify-email`, la del enlace del correo, solo con la verificación activa;
- un aviso de "confirmá tu email" cuando una respuesta trae `email_not_verified`;
- el segundo paso del login (el código del 2FA) cuando el login responde `202`. Si ese paso responde `401` (por ejemplo, `two_factor_locked`), volver a la pantalla de login;
- en el perfil, activar el 2FA (QR, código y códigos de recuperación) y desactivarlo;
- en el perfil, las sesiones por dispositivo (con "cerrar" y "cerrar todas las demás"), cambiar el email y borrar la cuenta;
- `/confirm-email`, la del enlace del correo para confirmar un email nuevo.

### Mismo dominio (recomendado)

Si el proxy (Caddy) sirve el frontend y la API bajo el mismo dominio, por ejemplo `cliente.com` y `cliente.com/api`, no hace falta CORS y la cookie funciona sin configurar nada:

```js
const res = await fetch("/api/auth/login", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ email, password }),
});
```

### Dominios distintos

Si el frontend y la API están en dominios distintos, por ejemplo `app.cliente.com` y `api.cliente.com`:

1. Configurá el origen del frontend en la API:

   ```env
   CORS_ALLOWED_ORIGINS=https://app.cliente.com
   FRONTEND_URL=https://app.cliente.com
   ```

2. Enviá la cookie en cada `fetch` con `credentials: "include"`:

   ```js
   const res = await fetch("https://api.cliente.com/auth/me", { credentials: "include" });
   if (res.status === 401) {
     // sin sesión: redirigir al login
   }
   ```

Con `SameSite=Lax`, el navegador envía la cookie entre **subdominios del mismo sitio** (`app.cliente.com` → `api.cliente.com`). Si el frontend estuviera en un sitio completamente distinto (`cliente-app.com` → `api.otro.com`), haría falta `SameSite=None`. No lo recomiendo: es mejor poner ambos bajo el mismo dominio.

### En desarrollo

Si Vite corre en `http://localhost:5173` y la API en `http://localhost:8080`, el sitio es el mismo (`localhost`), así que la cookie viaja. Para esto alcanza con:

```env
CORS_ALLOWED_ORIGINS=http://localhost:5173
FRONTEND_URL=http://localhost:5173
```

`COOKIE_SECURE` ya es `false` en desarrollo, así que la cookie funciona sin HTTPS.

## Proteger rutas nuevas

En el `routes.go` de cada recurso:

```go
mux.Handle("GET /orders", authn.RequireUser(httpx.Handle(h.list)))            // cualquier usuario (con el email verificado, si se exige)
mux.Handle("DELETE /orders/{id}", authn.RequireAdmin(httpx.Handle(h.delete))) // solo admin
mux.Handle("GET /products", httpx.Handle(h.list))                             // público
mux.Handle("GET /products", authn.OptionalUser(httpx.Handle(h.list)))         // público, pero sabe si hay usuario
```

Dentro del handler:

```go
user := auth.MustUser(r.Context())
```

## Quitar funciones

Todas las rutas están en `internal/auth/routes.go`. Para quitar una función, borrá sus rutas y su archivo:

| Función | Rutas | Archivo |
|---|---|---|
| Registro abierto (solo admins con `create-admin`) | `POST /auth/register` | `register` en `handlers.go` |
| Perfil y cambio de contraseña | `PUT /auth/me`, `PUT /auth/password` | `profile.go` |
| Sesiones por dispositivo | `/auth/sessions…` | `sessions.go` |
| Cambiar el email | `PUT /auth/email`, `/auth/email/confirm` | `email_change.go` y `emails/email_change*.txt` |
| Borrar la cuenta | `DELETE /auth/me` | `delete_account.go` y `emails/account_deleted.txt` |
| Recuperar la contraseña | `/auth/password/forgot`, `/auth/password/reset` | `password_reset.go` y `emails/password_reset.txt` |
| Verificar el email | `/auth/email/verify`, `/auth/email/resend` | `email_verification.go` y `emails/verify_email.txt` (y dejá `REQUIRE_EMAIL_VERIFICATION=false`) |
| 2FA | `/auth/two-factor/...` y el bloque `if u.TwoFactorEnabled` del login en `handlers.go` | `two_factor.go`, `two_factor_login.go` y `recovery_codes.go` (también se pueden borrar `internal/totp` y la tabla `user_recovery_codes`) |

Si no queda ninguna función que envíe correos, también se pueden borrar `internal/mail`, `emails.go`, `tokens.go`, la tabla `user_tokens` y las variables de correo.

Borrá también sus tests (`account_test.go`, `password_reset_test.go`, `email_verification_test.go`, `two_factor*_test.go`) y corré `make check` para confirmar que todo compila y pasa.

## Qué no incluye

- 2FA obligatorio (por rol o para todos).
- Llaves de seguridad físicas o passkeys (WebAuthn).
- Login con Google u otros proveedores (OAuth).
- Permisos más finos que `user`/`admin`.
