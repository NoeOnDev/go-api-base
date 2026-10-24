# Decisiones de diseño

Por qué la plantilla está armada así. Si cambiás alguna decisión, actualizá este archivo.

## Librería estándar primero

`net/http` (con las rutas por método y parámetro de Go 1.22), `encoding/json`, `log/slog` y `testing` cubren lo necesario para una API.
Menos dependencias significa menos actualizaciones, menos vulnerabilidades posibles y un código que se entiende sin conocer un framework.

**Dependencias del binario:** solo `pgx` (driver de PostgreSQL) y `goose` (migraciones).

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

Autenticación (sesiones con cookie), CORS, límite de peticiones y guía de despliegue (systemd + Caddy).
Se agregan cuando un proyecto los necesita.
