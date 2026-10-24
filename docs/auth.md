# Autenticación

La API usa **sesiones guardadas en PostgreSQL** y enviadas en una **cookie `HttpOnly`**. No usa JWT.

## Cómo funciona

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

| Método | Ruta | Cuerpo | Respuesta |
|---|---|---|---|
| `POST` | `/auth/register` | `{"name","email","password"}` | `201` con el usuario y la cookie; `409` si el email ya existe; `422` si hay datos inválidos |
| `POST` | `/auth/login` | `{"email","password"}` | `200` con el usuario y la cookie; `401` si los datos no coinciden |
| `POST` | `/auth/logout` | | `204` |
| `GET` | `/auth/me` | | `200` con el usuario; `401` sin sesión |

El usuario se devuelve así (nunca incluye la contraseña):

```json
{"id": 1, "name": "Ana", "email": "ana@ejemplo.com", "role": "user", "created_at": "..."}
```

**Reglas:**
- Nombre obligatorio, de hasta 100 caracteres.
- Email válido, que se guarda en minúsculas.
- Contraseña de 8 a 128 caracteres.

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
- **Login sin filtraciones:** siempre responde el mismo `401` y tarda lo mismo exista o no el email, así no se puede averiguar qué cuentas existen. El registro sí indica si un email está en uso (`409`), porque si no el usuario no sabría por qué falla.
- **Límite de intentos:** 5 seguidos por IP en `/auth/login` y `/auth/register`, y luego uno cada 12 segundos. Al superarlo responde `429` con el encabezado `Retry-After`.
- **Protección CSRF** (`http.CrossOriginProtection` de Go): rechaza con `403` los `POST`/`PUT`/`DELETE` que el navegador envía desde otro sitio. Las peticiones sin encabezados de navegador (`curl`, apps móviles) no se ven afectadas.
- **Limpieza de sesiones:** las sesiones vencidas se borran en cada login.

## Desde el frontend (React)

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
```

`COOKIE_SECURE` ya es `false` en desarrollo, así que la cookie funciona sin HTTPS.

## Proteger rutas nuevas

En el `routes.go` de cada recurso:

```go
mux.Handle("GET /orders", authn.RequireUser(httpx.Handle(h.list)))        // cualquier usuario con sesión
mux.Handle("DELETE /orders/{id}", authn.RequireAdmin(httpx.Handle(h.delete))) // solo admin
mux.Handle("GET /products", httpx.Handle(h.list))                         // público
```

Dentro del handler:

```go
user := auth.MustUser(r.Context())
```

## Qué no incluye

Recuperar la contraseña y verificar el email (los dos necesitan un servicio de envío de correos), login con Google u otros proveedores (OAuth), y permisos más finos que `user`/`admin`.
