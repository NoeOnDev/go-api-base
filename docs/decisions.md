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

## Herramientas en un módulo aparte (`tools/go.mod`)

sqlc, goose (la CLI) y govulncheck quedan fijados a una versión, sin instalar nada globalmente.
Están en un `go.mod` separado porque sqlc arrastra cientos de dependencias, y así el `go.mod` principal solo refleja lo que usa la API.

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

## argon2id para las contraseñas

Es el algoritmo recomendado por OWASP. Los parámetros (19 MiB, 2 iteraciones, 1 hilo) son los mínimos que recomiendan, elegidos porque el destino es un VPS de 1 vCPU.

Van guardados dentro de cada hash (formato PHC), así que se pueden subir más adelante sin invalidar las contraseñas existentes.

## Protección CSRF con la librería estándar

`http.CrossOriginProtection` (Go 1.25) rechaza las peticiones que modifican datos cuando el navegador indica que vienen de otro sitio (`Sec-Fetch-Site`, `Origin`). Junto con la cookie `SameSite=Lax`, reemplaza a los tokens CSRF clásicos sin que el frontend tenga que hacer nada.

Los orígenes de `CORS_ALLOWED_ORIGINS` se consideran confiables.

## Límite de peticiones en memoria

Un token bucket por IP (`golang.org/x/time/rate`) en `/auth/login` y `/auth/register`. Al estar en memoria, alcanza para una instancia. Con varias instancias, el límite habría que moverlo a la base, a Redis o al proxy.

La IP se lee de `X-Forwarded-For` solo con `TRUST_PROXY=true`, para que nadie pueda falsearla si la API está expuesta directamente.

## Ejemplos de recursos

Cada ejemplo muestra algo distinto, y entre los tres cubren los casos más comunes:
- `notes`: pertenencia por usuario.
- `categories`: escritura de admin, valor único y no borrar si está en uso.
- `products`: JOIN, filtros, búsqueda y FK inválida.

En un proyecto real se conservan los que sirvan y se borran los demás.

Un recurso ajeno responde **404 y no 403**, para no revelar que existe.

## Dinero en centavos

`price_cents bigint` en lugar de `float` (tiene errores de redondeo) o `numeric` (se mapea a un tipo incómodo en Go). El frontend divide por 100 para mostrarlo.

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

- Recuperar la contraseña y verificar el email (necesitan un servicio de correo).
- OAuth.
- Permisos más finos que `user`/`admin`.
- Guía de despliegue (systemd + Caddy).

Se agregan cuando un proyecto los necesita.
