# Tiempo real

La plantilla envía eventos al navegador con **Server-Sent Events (SSE)**: una conexión HTTP que queda abierta y por la que el servidor va mandando eventos. Funciona solo con la librería estándar de Go, pasa por cualquier proxy y el navegador se reconecta solo si se corta.

Cubre los casos más comunes:

| Caso | Cómo |
|---|---|
| Notificaciones dentro del sistema | `internal/notifications` (ya incluido) |
| Ver en vivo los cambios de otro colaborador (tipo Jira) | Un **tema** por tablero o proyecto |
| Cambios de estado (un pago, un proceso largo) | Un evento en el canal del usuario |
| La respuesta de un agente de IA mientras se genera | `realtime.Stream` en un `POST` |

## Cómo funciona

- **`GET /events`** (con sesión) abre el stream. Cada conexión escucha:
  - **el canal del usuario**, siempre: sus notificaciones y los cambios en sus datos;
  - **los temas que pida**, como `/events?topics=project:42,board:7`, si tiene permiso.
- El primer evento es **`ready`**, con los temas que escucha: `{"topics": ["project:42"]}`.
- Cada 25 segundos llega un latido (un comentario SSE, que el navegador ignora), para que el proxy no corte la conexión.
- **No se reenvían los eventos perdidos.** Si la conexión se corta, el navegador se reconecta solo y vuelve a recibir `ready`: ahí el frontend tiene que pedir el estado por REST. La fuente de verdad es siempre la base; los eventos solo avisan que algo cambió.

### Lo que ya publica la plantilla

| Evento | Canal | Datos |
|---|---|---|
| `notification` | del usuario | La notificación completa |
| `notification.read` | del usuario | `{"id": 5}` o `{"all": true}`, para actualizar el contador en las demás pestañas |
| `note.created`, `note.updated` | del dueño | La nota |
| `note.deleted` | del dueño | `{"id": 5}` |

Las notificaciones de seguridad (`password_changed`, `two_factor_enabled`, `two_factor_disabled`) las envía auth.

## Desde el frontend (React)

```js
// Una sola conexión para toda la app (por ejemplo, en un contexto de React).
const events = new EventSource("/api/events", { withCredentials: true });

// Se dispara al conectarse y cada vez que se reconecta: pedí el estado de nuevo.
events.addEventListener("ready", () => {
  queryClient.invalidateQueries(); // con TanStack Query, o el refetch que uses
});

events.addEventListener("notification", (e) => {
  const notification = JSON.parse(e.data);
  showToast(notification.data.message);
});

events.addEventListener("note.updated", (e) => {
  const note = JSON.parse(e.data);
  updateNoteInList(note);
});
```

`withCredentials: true` hace falta solo si el frontend está en otro dominio (con CORS configurado). En el mismo dominio, la cookie viaja sola.

**Para escuchar un tema**, abrí la conexión con `?topics=`. Si el usuario entra a otro tablero, cerrá la conexión (`events.close()`) y abrí otra con el tema nuevo.

## Publicar eventos

Desde un handler, con el broker (`server.Deps.Broker`, pasáselo al recurso al registrarlo):

```go
// Al canal de un usuario
h.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: "invoice.paid", Data: invoice})

// A un tema: todos los que lo estén mirando
h.broker.Publish(r.Context(), "project:42", realtime.Event{Type: "task.moved", Data: task})
```

- **Publicá después de guardar** (y, si hay transacción, después del `Commit`): si el evento llega antes que el dato, el frontend pide el estado y no lo encuentra.
- `Publish` no bloquea. Si una conexión no lee a tiempo (más de 64 eventos acumulados), se cierra y el navegador se reconecta.
- **Desde un worker** (`internal/jobs`) funciona igual: sumá el broker a `jobs.Deps`. Los workers corren en el mismo proceso, así que comparten el broker.
- Para una notificación que además quede guardada, usá `notifications.Service.Notify(ctx, userID, "kind", datos)`.

## Temas con permiso

Cada tipo de tema necesita un `Authorizer` que diga quién puede escucharlo. Un tema sin autorizador se rechaza con `403`, así nadie puede escuchar datos ajenos inventando un nombre. Se registra donde se registran las rutas del recurso:

```go
// internal/projects/routes.go
broker.Authorize("project", func(ctx context.Context, userID int64, id string) (bool, error) {
	projectID, err := strconv.ParseInt(id, 10, 64)
	if err != nil {
		return false, nil
	}
	return q.IsProjectMember(ctx, store.IsProjectMemberParams{ProjectID: projectID, UserID: userID})
})
```

El canal del usuario (`user:<id>`) no se puede pedir como tema: cada uno escucha solo el suyo.

## Transmitir la respuesta de un agente de IA

`realtime.Stream` sirve para cualquier respuesta larga que se envía de a partes. Para un chat, el frontend hace un `POST` con el mensaje, y la API responde con un stream:

```go
// POST /chat
func (h *Handler) chat(w http.ResponseWriter, r *http.Request) error {
	var in chatInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err // todavía se puede responder un error JSON normal
	}

	stream, err := realtime.Open(w, r)
	if err != nil {
		return err
	}
	// Desde acá la respuesta ya empezó (status 200): los errores van como evento.

	for chunk, err := range h.llm.Stream(r.Context(), in.Message) {
		if err != nil {
			_ = stream.Send(realtime.Event{Type: "error", Data: "no se pudo generar la respuesta"})
			return nil
		}
		select {
		case <-h.broker.Done():
			return nil // la API se está apagando
		default:
		}
		if err := stream.Send(realtime.Event{Type: "token", Data: chunk}); err != nil {
			return nil // el usuario cerró la pestaña: r.Context() también se cancela
		}
	}
	_ = stream.Send(realtime.Event{Type: "done"})
	return nil
}
```

- `Open` quita el `WriteTimeout` de 30 segundos para esta petición, así una respuesta larga no se corta.
- Si el usuario cierra la pestaña, `r.Context()` se cancela: pasalo a la llamada del modelo para dejar de generar (y de pagar tokens).
- Va por `POST`, así que pasa por la protección CSRF como cualquier otra ruta.

En el frontend, `EventSource` solo hace `GET`, así que la respuesta se lee con `fetch`:

```js
const res = await fetch("/api/chat", {
  method: "POST",
  headers: { "Content-Type": "application/json" },
  body: JSON.stringify({ message }),
});
const reader = res.body.pipeThrough(new TextDecoderStream()).getReader();
let buffer = "";
for (;;) {
  const { value, done } = await reader.read();
  if (done) break;
  buffer += value;
  const events = buffer.split("\n\n");
  buffer = events.pop(); // el último puede estar incompleto
  for (const raw of events) {
    const type = raw.match(/^event: (.*)$/m)?.[1];
    const data = raw.match(/^data: (.*)$/m)?.[1];
    if (type === "token") appendToMessage(JSON.parse(data));
  }
}
```

Librerías como `@microsoft/fetch-event-source` o el SDK de IA que uses hacen este parseo por vos.

## Límites

| Límite | Valor | Al superarlo |
|---|---|---|
| Conexiones por usuario | 10 (pestañas o dispositivos) | `429 too_many_connections` |
| Conexiones en total | 1000 | `429 too_many_connections` |
| Temas por conexión | 20 | `400` |

Cada conexión abierta ocupa unos **11 KB** de memoria (medido con 500 conexiones). Los valores están en `internal/realtime/broker.go`.

## Detrás de un proxy

- **Caddy:** no hace falta nada; envía los eventos apenas llegan.
- **Nginx:** la API ya envía `X-Accel-Buffering: no`, que le pide no guardar la respuesta en su buffer. El latido de 25 segundos queda por debajo de su `proxy_read_timeout` (60 segundos por defecto).
- **HTTP/2:** con HTTP/1.1, el navegador abre como mucho 6 conexiones por dominio, y cada pestaña con `EventSource` ocupa una. Con HTTPS, Caddy y Nginx usan HTTP/2, que no tiene ese límite.

## Una sola instancia

El broker vive en memoria: un evento publicado en una instancia solo llega a las conexiones de esa instancia. En un VPS con una sola instancia es lo correcto. Si algún día hay varias, se reparten los eventos con `LISTEN/NOTIFY` de PostgreSQL: `Publish` hace un `NOTIFY` y cada instancia escucha con `LISTEN` y reenvía a sus conexiones. La firma de `Publish` ya recibe `ctx` para poder cambiarlo sin tocar a quien publica.

## Por qué no WebSockets

SSE cubre todo lo de arriba: en esos casos el navegador envía poco (con REST normal) y el servidor avisa. Los WebSockets hacen falta cuando el navegador envía mucho y muy seguido, como cursores y trazos en un dibujo colaborativo. Ese caso necesita además resolver qué pasa cuando dos personas editan lo mismo a la vez (con técnicas como CRDT), y eso depende de cada proyecto. Por eso no vienen en la plantilla: se agregan en el proyecto que los necesite, y el broker les sirve igual.

## Quitar el tiempo real

Si un proyecto no lo necesita:

1. Borrá `internal/realtime` e `internal/notifications`, y la migración `00009_create_notifications.sql`.
2. En `internal/server/routes.go`, quitá el broker, el servicio de notificaciones, `GET /events` y `notifications.Register`. Pasale `nil` como `Notifier` a auth.
3. En `internal/notes`, quitá el broker y las llamadas a `PublishToUser`. En `cmd/api/main.go`, quitá el broker y `RegisterOnShutdown`.
4. `make sqlc` y `make check`.

Para quitar solo las notificaciones, hacé los pasos 1 y 2 únicamente con `internal/notifications`.
