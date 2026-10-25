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

**En producción, la cookie se llama `__Host-session`.** El navegador solo acepta ese prefijo si la cookie es `Secure`, tiene `Path=/` y no tiene `Domain`, así que un subdominio (por ejemplo, uno comprometido) no puede crearla ni pisarla. En desarrollo se llama `session`, porque el prefijo exige HTTPS.

**Encabezados:** todas las respuestas llevan `X-Content-Type-Options: nosniff`, y las de `/auth`, `Cache-Control: no-store`, porque algunas llevan secretos (el del 2FA, los códigos de recuperación). El resto de la API no lleva `no-store`, porque un listado público puede querer caché; un recurso con datos personales puede sumarlo con `httpx.NoStore`.

## Correos en segundo plano y por SMTP

- **SMTP con `net/smtp` de la librería estándar**, en lugar del SDK de un proveedor: funciona con cualquiera (Resend, Brevo, Amazon SES, Mailgun, el correo del hosting) y con Mailpit en desarrollo, y cambiar de proveedor es cambiar cuatro variables.
- **Pasan por la cola de trabajos** (`jobs.MailSender`): la respuesta no espera al servidor de correo, y si el SMTP falla o la API se reinicia, el correo no se pierde. Se reintenta hasta 5 veces, con una espera cada vez más larga.
- **El correo encolado va cifrado** con `secretbox`: los de recuperación llevan un enlace con token, y la tabla de la cola guarda los trabajos terminados 24 horas.
- **Como mucho 4 envíos a la vez** (la cola `email`): cada uno abre su propia conexión SMTP, y los proveedores rechazan muchas conexiones simultáneas.
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
- **El secreto se cifra con `secretbox`:** AES-256-GCM con una clave derivada de `ENCRYPTION_KEY` mediante HKDF. Así, un respaldo de la base filtrado no alcanza para generar códigos. El id del usuario va como dato asociado: un secreto copiado a otra fila no se puede descifrar.
- **Los códigos de recuperación se guardan como HMAC** con otra clave derivada, sobre el código y el id del usuario. Son aleatorios de unos 50 bits, así que un hash rápido alcanza si va con clave: sin `ENCRYPTION_KEY` no se pueden probar por fuerza bruta.
- **Se rechaza el reuso de un código** guardando el último paso usado, con un `UPDATE … WHERE totp_last_step < $2` atómico.
- **El login en dos pasos usa un desafío** en `user_tokens` y una cookie `HttpOnly`, no un token en el cuerpo de la respuesta. El frontend no maneja secretos, y el desafío vence en 5 minutos.
- **El 2FA es voluntario.** Obligarlo para ciertos roles es fácil de agregar en un proyecto (un control en `RequireAdmin`), pero cada proyecto lo decide.

## Claves rotables con identificador

`secretbox` es un llavero: una clave actual y las anteriores (`ENCRYPTION_PREVIOUS_KEYS`).

- **Cada dato cifrado lleva el identificador de su clave** (una huella derivada con HKDF), así la API sabe con cuál descifrar sin probarlas todas. Laravel, con `APP_PREVIOUS_KEYS`, sí prueba una tras otra.
- **La rotación es gradual:** cada secreto se vuelve a cifrar cuando su usuario usa el 2FA, y `rotate-keys` termina el resto.
- **El reporte indica cuándo se puede quitar una clave vieja**, para no hacerlo a ciegas.
- **Los códigos de recuperación guardan el `key_id` de su HMAC**, porque sin el código original no se puede saber con qué clave se calcularon.

Se descartó un servicio de claves externo (AWS KMS, Vault): en un VPS chico agrega costo y complejidad que no se justifican.

## argon2id para las contraseñas

Es el algoritmo recomendado por OWASP. Los parámetros (19 MiB, 2 iteraciones, 1 hilo) son los mínimos que recomiendan, elegidos porque el destino es un VPS de 1 vCPU.

Van guardados dentro de cada hash (formato PHC), así que se pueden subir más adelante sin invalidar las contraseñas existentes.

**Como mucho un hash por núcleo a la vez** (un semáforo en `password.go`). Cada hash reserva 19 MiB: sin tope, 200 logins simultáneos llevaban el proceso de 16 MiB a 2,5 GiB en 2 núcleos, y en un VPS chico el sistema lo habría matado. Con el tope, el pico queda en unos 120 MiB y los logins terminan incluso antes, porque la CPU ya no se reparte entre 200 hashes. Si el cliente se va mientras espera su turno, no se calcula nada.

## Protección CSRF con la librería estándar

`http.CrossOriginProtection` (Go 1.25) rechaza las peticiones que modifican datos cuando el navegador indica que vienen de otro sitio (`Sec-Fetch-Site`, `Origin`). Junto con la cookie `SameSite=Lax`, reemplaza a los tokens CSRF clásicos sin que el frontend tenga que hacer nada.

Los orígenes de `CORS_ALLOWED_ORIGINS` se consideran confiables.

## Límite de peticiones en memoria

Un token bucket (`golang.org/x/time/rate`) con cuatro usos:

- **Por IP**, en las rutas de auth: intentos (login, registro, contraseñas, 2FA) y correos.
- **Por cuenta, en el login:** 10 intentos seguidos por email y luego uno por minuto, vengan de la IP que vengan. Cuenta también los emails que no existen, para no revelar cuáles están registrados.
- **Por usuario, en el código del 2FA:** quien llega a ese paso ya tiene la contraseña y puede repartir sus intentos entre muchas IPs. Además, cada desafío admite 5 intentos (contados en la base, con un `UPDATE` atómico); después hay que volver a poner la contraseña, y se le avisa al usuario por correo.
- **Por destinatario, en los correos:** desde muchas IPs tampoco se le pueden mandar decenas de correos a una persona. En "olvidé mi contraseña" el límite es silencioso, para no revelar qué emails existen, y no invalida el último enlace enviado.

Al estar en memoria, alcanza para una instancia, y se reinicia con la API. Con varias instancias, los límites habría que moverlos a la base, a Redis o al proxy. Una clave inactiva se olvida recién cuando su límite ya se habría recargado.

El límite por cuenta tiene un costo: quien conozca un email puede bloquear ese login **mientras siga enviando intentos**, y le alcanza con uno por minuto. Recuperar la contraseña reinicia los límites del login y del 2FA, pero eso sirve cuando el ataque ya terminó: si continúa, el atacante vuelve a gastar los intentos. Se aceptó porque, sin el límite, un ataque repartido entre cientos de IPs prueba contraseñas sin freno, y porque el bloqueo dura solo lo que dura el ataque.

Si un proyecto necesita evitar ese bloqueo, la técnica habitual son las *device cookies* de OWASP: un dispositivo donde el usuario ya entró no cuenta para el límite.

La IP se lee de `X-Forwarded-For` solo con `TRUST_PROXY=true`, y aun así **solo si la conexión viene de localhost o de una red privada**, que es donde está el proxy: en el mismo servidor (`127.0.0.1`) o en una red de Docker (`172.x`). Si el puerto de la API quedara abierto a internet por error, quien se conecte directo no puede inventar su IP para esquivar los límites. Es el mismo criterio que usa Rails por defecto. Un proxy en otro servidor con IP pública no se cree: si un proyecto lo necesita, se agrega una lista de IPs de confianza.

La regla tiene un límite: confía en cualquier conexión desde una red privada. Con Docker y el puerto publicado a todas las interfaces, en algunas configuraciones (por ejemplo, IPv6 sin `ip6tables`) Docker reenvía las conexiones de afuera con `docker-proxy`, y la API las ve llegar desde el gateway (`172.17.0.1`), que es privado. Para la API, esa conexión es igual a la del proxy legítimo, así que el código no puede distinguirlas. Por eso el puerto de la API no debe quedar expuesto: sin publicarlo, o publicado solo en `127.0.0.1` (ver "Antes de producción" en el README).

Las IPv6 se agrupan por bloque `/64`: un proveedor le asigna a cada cliente un `/64` entero, así que contar por dirección exacta permitiría esquivar el límite rotando direcciones.

## La plantilla no es una dependencia

`gonew` copia el código, y los proyectos creados no quedan conectados a la plantilla: mejorarla no rompe nada en ellos. Por eso en este repositorio se puede reorganizar libremente, incluidas las migraciones, que representan el esquema inicial.

Para saber de qué versión salió cada proyecto, la plantilla se versiona con tags (`v0.1.0`, …) y un `CHANGELOG.md`. Llevar una mejora a un proyecto existente se hace a mano, mirando el cambio en el historial. Cuando una versión cambia el esquema, el CHANGELOG trae el SQL para la migración nueva del proyecto.

**La misma regla vale en cada proyecto:** mientras no esté desplegado, las migraciones originales se editan (sumar `ALTER TABLE` por cada campo solo ensucia el historial). Después del primer despliegue, cada cambio va en una migración nueva, porque la base de producción ya aplicó las anteriores.

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

## Trabajos en segundo plano con River

[River](https://riverqueue.com) guarda la cola en PostgreSQL. Se eligió frente a:
- **Una cola propia** (con `SELECT … FOR UPDATE SKIP LOCKED`): parece simple, pero los reintentos, los trabajos trabados, los periódicos sin duplicados y el apagado ordenado son cientos de líneas difíciles de probar. River lo resuelve y es una dependencia chica (usa el mismo pgx).
- **Redis** (con Asynq, por ejemplo): suma otro servicio que instalar, respaldar y vigilar en el VPS. Además, PostgreSQL permite encolar dentro de la misma transacción que los datos, algo que con Redis no se puede.

Decisiones:
- **Los workers corren dentro del proceso de la API.** Un solo binario y un solo servicio; se pueden separar si un proyecto lo necesita (ver `docs/jobs.md`).
- **Su esquema entra como migración de goose** (exportado con su CLI), no con el migrador de River: goose sigue siendo la única fuente del esquema.
- **La limpieza de sesiones y tokens vencidos es un trabajo periódico**, en lugar de hacerse en cada login.
- **No se incluye River UI**, su interfaz web: suma otra dependencia y necesita su propia autenticación.

## Tiempo real con SSE

Server-Sent Events, con la librería estándar, en lugar de WebSockets:
- **Cubren los casos habituales:** notificaciones, ver en vivo los cambios de otros (tipo Jira), cambios de estado y la respuesta de un agente de IA mientras se genera. En todos, el navegador envía poco (por REST) y el servidor avisa.
- **Sin dependencias ni protocolo nuevo:** es HTTP normal. Usa la misma cookie de sesión, pasa por los mismos middlewares y por cualquier proxy, y el navegador se reconecta solo.
- **Los WebSockets hacen falta** cuando el navegador envía mucho y muy seguido (un dibujo colaborativo). Ese caso necesita además resolver ediciones simultáneas, que depende de cada proyecto, así que se agregan ahí.

Decisiones:
- **Los temas necesitan un autorizador:** un tema sin autorizador se rechaza, así no se puede escuchar lo ajeno inventando un nombre.
- **No se reenvían los eventos perdidos.** Al reconectarse, el frontend pide el estado por REST: es más simple y no pierde nada, porque la fuente de verdad es la base.
- **El stream quita el `WriteTimeout` solo para su petición**, y al apagar la API se cierran todos (`RegisterOnShutdown`); si no, el apagado esperaría hasta el timeout.
- **Límites de 10 conexiones por usuario y 1000 en total**, porque cada una es un socket y una goroutine abiertos. Cada conexión ocupa unos 11 KB.
- **El broker vive en memoria:** alcanza para una instancia. Con varias, se reparte con `LISTEN/NOTIFY` (ver `docs/realtime.md`).

## Qué no incluye

- Cambiar el email.
- 2FA obligatorio, y passkeys o WebAuthn.
- Login con Google u otros proveedores (OAuth).
- Permisos más finos que `user`/`admin`.
- WebSockets (ver "Tiempo real con SSE").

No es una lista de pendientes: son funciones que dependen de cada proyecto, y se agregan en el proyecto que las necesite.
