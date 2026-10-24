# Decisiones de diseño

Por qué la plantilla está armada así. Si cambiás alguna decisión, actualizá este archivo.

## Librería estándar primero

`net/http` (con las rutas por método y parámetro de Go 1.22), `encoding/json`, `log/slog` y `testing` cubren lo necesario para una API.
Menos dependencias significa menos actualizaciones, menos vulnerabilidades posibles y un código que se entiende sin conocer un framework.

**Dependencias del binario:**
- `pgx`: driver de PostgreSQL.
- `goose`: migraciones.
- Tres paquetes del equipo de Go:
  - `golang.org/x/crypto`: argon2id;
  - `golang.org/x/time`: límite de peticiones;
  - `golang.org/x/term`: leer la contraseña en `create-admin` sin mostrarla.

## pgx + sqlc en lugar de un ORM

- Escribís SQL normal, y sqlc genera funciones Go con tipos a partir de él. Los errores de SQL aparecen al generar, no en producción.
- No hay consultas ocultas ni problemas N+1 inesperados.
- pgx es el driver más completo y rápido para PostgreSQL en Go.

## Migraciones con goose, embebidas y aplicadas al arrancar

- Son archivos `.sql` versionados, fáciles de revisar.
- Van dentro del binario (`embed`): para desplegar alcanza con copiar un solo archivo.
- La API migra al arrancar, con un advisory lock de PostgreSQL para que dos instancias no migren a la vez. Es simple y suficiente para un VPS.
- sqlc lee las mismas migraciones como esquema, así que son la única fuente de verdad.

## Herramientas con versión fija en el Makefile

sqlc, goose (la CLI) y govulncheck se ejecutan con `go run paquete@versión`, con la versión en una variable del `Makefile` (`SQLC_VERSION`, etc.):
- **No hace falta instalar nada:** la primera vez se descargan, verificadas contra la base de checksums de Go, y se compilan; después se usan desde la caché.
- **No agregan dependencias al `go.mod` de la API.** sqlc sola arrastra cientos.
- **Funcionan en proyectos creados con `gonew`.** Un módulo aparte (por ejemplo `tools/go.mod`) no sirve: Go excluye las subcarpetas con su propio `go.mod` al empaquetar el módulo, así que `gonew` no las copia.

Para actualizar una herramienta, cambiá su versión en el `Makefile`.

## Formato único de errores

```json
{"error": {"code": "validation_failed", "message": "...", "fields": {"title": "es obligatorio"}}}
```

El frontend maneja todos los errores igual. Los errores 500 no exponen detalles; la causa real queda en el log con su `request_id`.

## Handlers que devuelven error

`func(w, r) error` más `httpx.Handle` centraliza el manejo de errores: cada handler termina con `return err` y no se repite código para responder errores.

## Respuestas separadas de los structs de la base

Cada recurso define su struct de respuesta. Agregar una columna a la tabla no cambia la API sin que lo decidas, y nunca se expone un campo por accidente (por ejemplo, un hash de contraseña).

## Sesiones con cookie en lugar de JWT

- **Más seguras para un frontend web:** la cookie `HttpOnly` no se puede leer desde JavaScript, a diferencia de un JWT guardado en `localStorage`.
- **Se pueden revocar al instante** (logout, cambio de contraseña, bloquear un usuario): basta con borrar la fila. Un JWT sigue siendo válido hasta que vence.
- **El costo es una consulta por petición** (`GetSessionUser`, por clave primaria). Es despreciable a esta escala.
- **En la base se guarda el SHA-256 del token**, no el token: si alguien lee la tabla, no puede usar las sesiones.

JWT conviene cuando varios servicios validan sesiones sin acceso a la base, o para APIs de terceros. Ninguno de los dos es el caso de esta plantilla.

## Correos en segundo plano y por SMTP

- **SMTP con `net/smtp` de la librería estándar**, en lugar del SDK de un proveedor: funciona con cualquiera (Resend, Brevo, Amazon SES, Mailgun, el correo del hosting) y con Mailpit en desarrollo, y cambiar de proveedor es cambiar cuatro variables.
- **Se envían en segundo plano** (`mail.Queue`), por dos motivos:
  - la respuesta no espera al servidor de correo;
  - "olvidé mi contraseña" tarda lo mismo exista o no el email.
- **Si el envío falla**, queda en el log; al apagar la API se esperan los pendientes.
- **No hay reintentos ni cola persistente.** Para correos críticos o de mucho volumen, conviene una cola en la base o un servicio dedicado.
- **Plantillas en texto plano**, embebidas en el binario. Se leen bien en cualquier cliente y no tienen problemas de compatibilidad con HTML.

## Tokens de un solo uso por correo

Recuperar la contraseña y verificar el email usan la tabla `user_tokens`:
- **Como en las sesiones**, se guarda solo el SHA-256 del token.
- **Vencen** a la hora (recuperación) o a las 24 horas (verificación).
- **Solo sirve el último enlace:** pedir uno nuevo invalida el anterior.
- **No se pueden usar dos veces:** se validan y borran en una sola sentencia (`DELETE … RETURNING`), así que ni dos peticiones simultáneas pueden reutilizarlos.

Los enlaces apuntan al **frontend** (`FRONTEND_URL`), no a la API: la pantalla toma el token de la URL y llama a la API. Así la API sigue siendo solo JSON.

## Verificación de email opcional

Viene desactivada (`REQUIRE_EMAIL_VERIFICATION=false`), porque muchos proyectos privados no la necesitan.

Activada, bloquea las rutas protegidas con un `403 email_not_verified` en lugar de impedir el login. Así el usuario puede entrar, ver el aviso y pedir otro enlace sin quedar trabado.

## 2FA con TOTP propio

- **TOTP (RFC 6238) implementado con la librería estándar** (unas 100 líneas en `internal/totp`, probadas con los vectores oficiales del RFC), en lugar de sumar una dependencia.
- **El QR lo dibuja el frontend** a partir de `otpauth_url`, porque generar la imagen en Go necesitaría otra librería.
- **El secreto se cifra con `secretbox`:** AES-256-GCM con una clave derivada de `ENCRYPTION_KEY` mediante HKDF. Así, un respaldo de la base filtrado no alcanza para generar códigos.
- **Los códigos de recuperación se guardan como HMAC** con otra clave derivada. Son aleatorios de unos 50 bits, así que un hash rápido alcanza si va con clave: sin `ENCRYPTION_KEY` no se pueden probar por fuerza bruta.
- **Se rechaza el reuso de un código** guardando el último paso usado, con un `UPDATE … WHERE totp_last_step < $2` atómico.
- **El login en dos pasos usa un desafío** en `user_tokens` y una cookie `HttpOnly`, no un token en el cuerpo de la respuesta. El frontend no maneja secretos, y el desafío vence en 5 minutos.
- **El 2FA es voluntario.** Obligarlo para ciertos roles es fácil de agregar en un proyecto (un control en `RequireAdmin`), pero cada proyecto lo decide.

## argon2id para las contraseñas

Es el algoritmo recomendado por OWASP. Los parámetros (19 MiB, 2 iteraciones, 1 hilo) son los mínimos que recomiendan, elegidos porque el destino es un VPS de 1 vCPU.

Van guardados dentro de cada hash (formato PHC), así que se pueden subir más adelante sin invalidar las contraseñas existentes.

## Protección CSRF con la librería estándar

`http.CrossOriginProtection` (Go 1.25) rechaza las peticiones que modifican datos cuando el navegador indica que vienen de otro sitio (`Sec-Fetch-Site`, `Origin`). Junto con la cookie `SameSite=Lax`, reemplaza a los tokens CSRF clásicos sin que el frontend tenga que hacer nada.

Los orígenes de `CORS_ALLOWED_ORIGINS` se consideran confiables.

## Límite de peticiones en memoria

Un token bucket por IP (`golang.org/x/time/rate`) en `/auth/login` y `/auth/register`. Al estar en memoria, alcanza para una instancia. Con varias instancias, el límite habría que moverlo a la base, a Redis o al proxy.

La IP se lee de `X-Forwarded-For` solo con `TRUST_PROXY=true`, para que nadie pueda falsearla si la API está expuesta directamente.

Las IPv6 se agrupan por bloque `/64`: un proveedor le asigna a cada cliente un `/64` entero, así que contar por dirección exacta permitiría esquivar el límite rotando direcciones.

## La plantilla no es una dependencia

`gonew` copia el código, y los proyectos creados no quedan conectados a la plantilla: mejorarla no rompe nada en ellos. Por eso en este repositorio se puede reorganizar libremente, incluidas las migraciones, que representan el esquema inicial.

Para saber de qué versión salió cada proyecto, la plantilla se versiona con tags (`v0.1.0`, …) y un `CHANGELOG.md`. Llevar una mejora a un proyecto existente se hace a mano, mirando el cambio en el historial.

## Ejemplos de recursos

Cada ejemplo muestra algo distinto, y entre los tres cubren los casos más comunes:
- `notes`: pertenencia por usuario.
- `categories`: escritura de admin, valor único y no borrar si está en uso.
- `products`: JOIN, filtros, búsqueda y FK inválida.

En un proyecto real se conservan los que sirvan y se borran los demás.

Un recurso ajeno responde **404 y no 403**, para no revelar que existe.

## Dinero en centavos

`price_cents bigint` en lugar de `float` (tiene errores de redondeo) o `numeric` (se mapea a un tipo incómodo en Go). El frontend divide por 100 para mostrarlo.

## Base de pruebas separada y protegida

Los tests usan **solo** `TEST_DATABASE_URL`, una base distinta de la de desarrollo (`DATABASE_URL`), así que nunca tocan los datos que usás para probar a mano o mostrar demos.

Como cada test vacía todas las tablas, `testutil.NewDB` se niega a correr si la base de pruebas no termina en `_test` o si es la misma que `DATABASE_URL`. Así, un error al copiar variables no puede borrar la base de desarrollo, ni mucho menos la de producción. Es la misma convención que usan Rails y Django.

## Tests de integración en serie

Los paquetes comparten la base de pruebas y cada test la vacía al empezar. Por eso `make test` usa `go test -p 1`. Con la escala actual, todos los tests tardan alrededor de un segundo y medio.

## IDs bigint

`bigint GENERATED ALWAYS AS IDENTITY` funciona en cualquier versión de PostgreSQL y es eficiente.
Si necesitás IDs no adivinables, una alternativa es UUIDv7: PostgreSQL 18 lo genera de forma nativa con `uuidv7()`.

## Logs con slog

En desarrollo se usa texto legible y en producción JSON, que es lo que procesan las herramientas de logs.
Cada línea de una petición lleva su `request_id`, que también se devuelve en el encabezado `X-Request-ID`. Así, un error que reporta un usuario se puede encontrar en los logs.

## Timeouts del servidor

`ReadHeaderTimeout` 5 s, `ReadTimeout` 15 s, `WriteTimeout` 30 s, `IdleTimeout` 60 s.
Sin ellos, un cliente lento puede mantener conexiones abiertas indefinidamente.

## Rutas inexistentes

Cualquier ruta no registrada responde 404 en JSON. Un método no permitido en una ruta existente (por ejemplo `POST /healthz`) también responde 404, porque la ruta comodín `/` tiene prioridad sobre el 405 de `net/http`. Es una simplificación aceptada.

## Versión de Go

`go.mod` pide Go 1.27.3, que incluye las correcciones de seguridad de octubre de 2026 (`crypto/x509`, `net/http`, `crypto/tls`).
Con `GOTOOLCHAIN=auto` (el valor por defecto), una versión anterior de Go descarga la correcta automáticamente.

## Qué no incluye (todavía)

- Cambiar el email.
- 2FA obligatorio, y passkeys o WebAuthn.
- OAuth.
- Permisos más finos que `user`/`admin`.
- Guía de despliegue (systemd + Caddy).

Se agregan cuando un proyecto los necesita.
