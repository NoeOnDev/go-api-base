# Autenticación

La API usa **sesiones guardadas en PostgreSQL** y enviadas en una **cookie `HttpOnly`**. No usa JWT.

Incluye:
- registro, login y logout;
- perfil y cambio de contraseña;
- recuperar la contraseña por correo;
- verificar el email (opcional);
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

## Endpoints

| Método | Ruta | Acceso | Cuerpo | Respuesta |
|---|---|---|---|---|
| `POST` | `/auth/register` | público | `{name, email, password}` | `201` con el usuario y la cookie; `409` si el email ya existe; `422` |
| `POST` | `/auth/login` | público | `{email, password}` | `200` con el usuario y la cookie; `401` si los datos no coinciden |
| `POST` | `/auth/logout` | público | | `204`, siempre (también si la sesión ya venció) |
| `GET` | `/auth/me` | sesión | | `200` con el usuario |
| `PUT` | `/auth/me` | sesión | `{name}` | `200` con el usuario |
| `PUT` | `/auth/password` | usuario | `{current_password, password}` | `204`; `422` si la contraseña actual es incorrecta |
| `POST` | `/auth/password/forgot` | público | `{email}` | `202`, siempre |
| `POST` | `/auth/password/reset` | público | `{token, password}` | `204`; `422` si el enlace no es válido o venció |
| `POST` | `/auth/email/verify` | público | `{token}` | `204`; `422` si el enlace no es válido o venció |
| `POST` | `/auth/email/resend` | sesión | | `202`; `204` si el email ya estaba verificado |

En la columna de acceso:
- **sesión:** cualquier usuario logueado, aunque no haya verificado su email.
- **usuario:** además exige el email verificado, si `REQUIRE_EMAIL_VERIFICATION=true`.

El usuario se devuelve así (nunca incluye la contraseña):

```json
{"id": 1, "name": "Ana", "email": "ana@ejemplo.com", "role": "user", "email_verified_at": null, "created_at": "..."}
```

**Reglas:**
- Nombre obligatorio, de hasta 100 caracteres.
- Email válido, que se guarda en minúsculas.
- Contraseña de 8 a 128 caracteres.

## Perfil y cambio de contraseña

- **`PUT /auth/me`** cambia el nombre. El email no se cambia por acá: cambiarlo exige verificar la dirección nueva, y todavía no está incluido.
- **`PUT /auth/password`** pide la contraseña actual, así alguien que encuentre la sesión abierta no puede cambiarla. Al cambiarla se **cierran todas las demás sesiones** del usuario y la actual sigue activa.

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

## Correos

| Variable | Para qué |
|---|---|
| `MAIL_DRIVER` | `log`: escribe los correos en el log, para desarrollo sin servidor. `smtp`: los envía |
| `MAIL_FROM` | Remitente |
| `SMTP_HOST`, `SMTP_PORT`, `SMTP_USERNAME`, `SMTP_PASSWORD` | Datos SMTP de tu proveedor (Resend, Brevo, Amazon SES, Mailgun o el correo del hosting), normalmente con el puerto 587 |
| `FRONTEND_URL` | Base de los enlaces de los correos |

**Cómo se envían:**
- **En segundo plano** (`mail.Queue`): la respuesta HTTP no espera al servidor de correo. Al apagar la API se esperan los envíos pendientes.
- **Si un envío falla**, queda registrado en el log.
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
- **Límites por IP** (las IPv6 se cuentan por bloque `/64`). Al superarlos se responde `429` con `Retry-After`:
  - **Intentos:** login, registro, cambio de contraseña, reset y verificación. 5 seguidos y luego uno cada 12 segundos.
  - **Correos:** `forgot` y `resend`. 3 seguidos y luego uno cada 5 minutos.
- **Protección CSRF** (`http.CrossOriginProtection` de Go): rechaza con `403` los `POST`/`PUT`/`DELETE` que el navegador envía desde otro sitio. Las peticiones sin encabezados de navegador (`curl`, apps móviles) no se ven afectadas.
- **Limpieza:** las sesiones y los tokens vencidos se borran al crear nuevos.

## Desde el frontend (React)

**Pantallas que necesita el frontend:**
- login y registro;
- perfil (nombre y cambio de contraseña);
- "olvidé mi contraseña";
- `/reset-password`, la del enlace del correo;
- `/verify-email`, la del enlace del correo, solo con la verificación activa;
- un aviso de "confirmá tu email" cuando una respuesta trae `email_not_verified`.

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
| Recuperar la contraseña | `/auth/password/forgot`, `/auth/password/reset` | `password_reset.go` y `emails/password_reset.txt` |
| Verificar el email | `/auth/email/verify`, `/auth/email/resend` | `email_verification.go` y `emails/verify_email.txt` (y dejá `REQUIRE_EMAIL_VERIFICATION=false`) |

Si no queda ninguna función que envíe correos, también se pueden borrar `internal/mail`, `emails.go`, `tokens.go`, la tabla `user_tokens` y las variables de correo.

Borrá también sus tests (`account_test.go`, `password_reset_test.go`, `email_verification_test.go`) y corré `make check` para confirmar que todo compila y pasa.

## Qué no incluye

- Cambiar el email.
- 2FA.
- Login con Google u otros proveedores (OAuth).
- Permisos más finos que `user`/`admin`.
