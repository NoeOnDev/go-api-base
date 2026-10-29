# Real time

The template sends events to the browser with **Server-Sent Events (SSE)**: an HTTP connection that stays open and through which the server keeps sending events. It works with Go's standard library alone, goes through any proxy and the browser reconnects on its own if it is cut.

It covers the most common cases:

| Case | How |
|---|---|
| In-app notifications | `internal/notifications` (already included) |
| Seeing another collaborator's changes live (like Jira) | A **topic** per board or project |
| State changes (a payment, a long process) | An event on the user's channel |
| An AI agent's response while it is generated | `realtime.Stream` in a `POST` |

## How it works

- **`GET /events`** (with a session) opens the stream. Each connection listens to:
  - **the user's channel**, always: their notifications and the changes in their data;
  - **the topics it asks for**, like `/events?topics=project:42,board:7`, if it has permission.
- The first event is **`ready`**, with the topics it listens to and those rejected for lack of permission: `{"topics": ["board:7"], "rejected": ["project:42"]}`. A rejected topic does not cut the connection: the user keeps receiving their channel and the other topics.
- Every 25 seconds a heartbeat arrives (an SSE comment, which the browser ignores), so the proxy does not cut the connection.
- **The stream is cut when its session is closed:**
  - at once, with the logout of that session, or when the user's other sessions are closed (on a password change or reset and when 2FA is turned on or off). That way, whoever has a stolen session stops receiving events before the "your password was changed" notice;
  - on the next heartbeat (25 seconds at most), if the session expired or was deleted some other way;
  - and after 15 minutes anyway (between 13 and 17, at random, so that after a restart not every tab renews at once). The browser reconnects on its own, and on reconnection the session and the permissions of each topic are checked again.
- **Lost events are not resent.** If the connection is cut, the browser reconnects on its own and receives `ready` again: that is when the frontend must ask for the state over REST. The source of truth is always the database; the events only say that something changed.

### What the template already publishes

| Event | Channel | Data |
|---|---|---|
| `notification` | the user's | The whole notification |
| `notification.read` | the user's | `{"id": 5}` or `{"all": true}`, to update the counter in the other tabs |
| `note.created`, `note.updated` | the owner's | The note |
| `note.deleted` | the owner's | `{"id": 5}` |

The security notifications (`password_changed`, `two_factor_enabled`, `two_factor_disabled`) are sent by auth.

## From the frontend (React)

```js
// A single connection for the whole app (for example, in a React context). It is built in a
// function so it can be created again, with its listeners, if it has to be reconnected by hand.
let events;

function connect(topics = []) {
  const query = topics.length > 0 ? `?topics=${topics.join(",")}` : "";
  events = new EventSource(`/api/events${query}`, { withCredentials: true });

  // Fires on connecting and every time it reconnects: ask for the state again.
  events.addEventListener("ready", (e) => {
    queryClient.invalidateQueries(); // with TanStack Query, or the refetch you use

    // Topics it can no longer listen to (for example, they removed them from the project they are viewing).
    const { rejected } = JSON.parse(e.data);
    if (rejected.length > 0) showLostAccess(rejected);
  });

  events.addEventListener("notification", (e) => {
    const notification = JSON.parse(e.data);
    showToast(notification.message); // already in the user's language (see docs/i18n.md)
  });

  events.addEventListener("note.updated", (e) => {
    const note = JSON.parse(e.data);
    updateNoteInList(note);
  });

  events.onerror = () => handleClosed(topics);
}
```

`withCredentials: true` is needed only if the frontend is on another domain (with CORS configured). On the same domain, the cookie travels on its own.

**Open the connection when the user can already use the app.** `GET /events` is behind `RequireUser`, so with `REQUIRE_EMAIL_VERIFICATION=true` it responds `403 email_not_verified` while the email is not confirmed. Call `connect()` after the login, only if `email_verified_at` is not `null` (or if verification is not required).

**When the connection cannot be opened**, the `EventSource` stops retrying and stays with `readyState === EventSource.CLOSED`. It happens with a closed session (`401`), with an unverified email when verification is required (`403`), with too many open connections (`429`) or with the API restarting (`503`). The browser does not say which one it was, so it is best to ask the API with a route that goes through the same checks as `/events` (`RequireUser`), like `/notifications`:

```js
async function handleClosed(topics) {
  if (events.readyState !== EventSource.CLOSED) return; // a network glitch: it reconnects on its own

  const res = await fetch("/api/notifications?limit=1", { credentials: "include" });
  if (res.status === 401) {
    redirectToLogin(); // the session was closed
    return;
  }
  if (res.status === 403 && (await res.json()).error.code === "email_not_verified") {
    showVerifyEmailNotice(); // do not retry: it will keep failing until the email is confirmed
    return;
  }
  // The session and the email are fine: too many connections, or the API restarting.
  setTimeout(() => connect(topics), 10_000);
}
```

That way the frontend does not need to know whether verification is required: the response already says so. If you removed the notifications, any other route with `RequireUser` works, like `/notes`.

A topic without permission does not end up here: it is reported in `ready` and the connection stays open.

**To listen to a topic**, open the connection with `connect(["project:42"])`. If the user goes to another board, close the connection (`events.close()`) and open another one with the new topic.

## Publishing events

From a handler, with the broker (`server.Deps.Broker`, pass it to the resource when registering it):

```go
// To a user's channel
h.broker.PublishToUser(r.Context(), user.ID, realtime.Event{Type: "invoice.paid", Data: invoice})

// To a topic: everyone who is watching it
h.broker.Publish(r.Context(), "project:42", realtime.Event{Type: "task.moved", Data: task})
```

- **Publish after saving** (and, with a transaction, after the `Commit`): if the event arrives before the data, the frontend asks for the state and does not find it.
- `Publish` does not block. If a connection does not read in time (more than 64 pending events), it is closed and the browser reconnects.
- **From a worker** (`internal/jobs`) it works the same: add the broker to `jobs.Deps`. The workers run in the same process, so they share the broker.
- For a notification that is also stored, use `notifications.Service.Notify(ctx, userID, "kind", data)`.

## Topics with permissions

Each topic kind needs an `Authorizer` that says who can listen to it. A topic without an authorizer, or one the authorizer does not allow, is rejected: it arrives in `rejected` of the `ready` event and is not listened to. That way nobody can listen to other people's data by making up a name. It is registered where the resource's routes are registered:

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

The user's channel (`user:<id>`) cannot be asked for as a topic: each user listens only to their own.

**When you take away someone's access** (for example, removing them from a project), call `broker.DisconnectUser(ctx, userID)`: they reconnect on their own, and since they no longer have permission, the topic arrives in `rejected`, while they keep receiving their notifications. If you don't, they stop listening to it anyway in about 15 minutes, when their stream renews.

## Streaming an AI agent's response

`realtime.Stream` works for any long response sent in parts. For a chat, the frontend makes a `POST` with the message, and the API responds with a stream:

```go
// POST /chat
func (h *Handler) chat(w http.ResponseWriter, r *http.Request) error {
	var in chatInput
	if err := httpx.Decode(w, r, &in); err != nil {
		return err // a normal JSON error can still be returned
	}

	stream, err := realtime.Open(w, r)
	if err != nil {
		return err
	}
	// From here on the response has started (status 200): errors go as an event.

	for chunk, err := range h.llm.Stream(r.Context(), in.Message) {
		if err != nil {
			_ = stream.Send(realtime.Event{Type: "error", Data: "generation_failed"})
			return nil
		}
		select {
		case <-h.broker.Done():
			return nil // the API is shutting down
		default:
		}
		if err := stream.Send(realtime.Event{Type: "token", Data: chunk}); err != nil {
			return nil // the user closed the tab: r.Context() is cancelled too
		}
	}
	_ = stream.Send(realtime.Event{Type: "done"})
	return nil
}
```

- `Open` removes the 30-second `WriteTimeout` for this request, so a long response is not cut.
- If the user closes the tab, `r.Context()` is cancelled: pass it to the model call to stop generating (and paying for tokens).
- It goes over `POST`, so it goes through the CSRF protection like any other route.
- The error event carries a code (`generation_failed`), not a text, because no user-facing text goes in the code: the frontend maps it to its message. To send the text instead, add a key to the catalogs and translate it with `i18n.FromContext(r.Context())` (see [docs/i18n.md](i18n.md)).

In the frontend, `EventSource` only does `GET`, so the response is read with `fetch`:

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
  buffer = events.pop(); // the last one may be incomplete
  for (const raw of events) {
    const type = raw.match(/^event: (.*)$/m)?.[1];
    const data = raw.match(/^data: (.*)$/m)?.[1];
    if (type === "token") appendToMessage(JSON.parse(data));
  }
}
```

Libraries like `@microsoft/fetch-event-source`, or the AI SDK you use, do this parsing for you.

## Limits

| Limit | Value | When exceeded |
|---|---|---|
| Connections per user | 10 (tabs or devices) | `429 too_many_connections` |
| Connections in total | 1000 | `429 too_many_connections` |
| Topics per connection | 20 | `400` |

Each open connection takes about **11 KB** of memory (measured with 500 connections). The values are in `internal/realtime/broker.go`, and they are per API instance.

## Behind a proxy

- **No buffering:** the proxy must forward each event as soon as it arrives, without batching them. The API sends `X-Accel-Buffering: no`, which some proxies honor to avoid buffering the response.
- **Heartbeat:** every 25 seconds, below the 60 that proxies usually wait before cutting a connection without traffic.
- **HTTP/2:** with HTTP/1.1, the browser opens at most 6 connections per domain, and each tab with an `EventSource` takes one. With HTTP/2, the usual with HTTPS, there is no such limit.

## Multiple instances

Each API instance has its broker, with its connections. With `Listen` (called by `cmd/api/main.go`), the brokers notify each other with PostgreSQL's `LISTEN/NOTIFY`. That way, an event published on any instance reaches the connections of all of them, and so do the session closings (`SessionEnded`, `UserSessionsEnded`, `DisconnectUser`). The publisher does not have to do anything different. The code is in `internal/realtime/instances.go`.

- **Each instance delivers its own at once** and sends the message by `NOTIFY`. The others receive it and deliver it to their connections; the one that sent it ignores it.
- **Large events:** a `NOTIFY` allows less than 8000 bytes. A larger message, like the event of a long note, is stored in the `realtime_messages` table, and only its id travels by `NOTIFY`. The hourly cleanup deletes the old ones. There is no need to watch the size, although a small event (ids and what changed) is still best.
- **If the `LISTEN` connection is cut** (PostgreSQL restarts, the network fails), it reconnects on its own. On reconnecting it cuts the streams open on that instance: some event may have been lost while it was not listening, and on reconnecting the browser asks for the state again.
- **It uses its own connection**, apart from the pool, which shows up in `pg_stat_activity` as `realtime-listener`. It needs a direct connection to PostgreSQL, or PgBouncer in `session` mode.
- **The connection limits are per instance:** with 3 instances, a user can reach 30.
- In the tests, the broker lives only in memory (without `Listen`). `internal/realtime/instances_test.go` tests two instances on the same database.

## Why not WebSockets

SSE covers everything above: in those cases the browser sends little (with normal REST) and the server notifies. WebSockets are needed when the browser sends a lot and very often, like cursors and strokes in a collaborative drawing. That case also needs to solve what happens when two people edit the same thing at once (with techniques like CRDTs), and that depends on each project. That is why they are not in the template: they are added in the project that needs them, and the broker works for them too.

## Removing real time

If a project does not need it:

1. Delete `internal/realtime` and `internal/notifications`, the `00009_create_notifications.sql` and `00011_create_realtime_messages.sql` migrations, and the `notifications.sql` and `realtime.sql` queries of `internal/db/queries`.
2. In `internal/server/routes.go`, remove the `Broker` field of `Deps`, the broker, the notifications service, `GET /events` and `notifications.Register`. In the auth options, remove `Notifier` and `SessionWatcher` (they are optional).
3. In `internal/notes`, remove the broker, the `PublishToUser` calls and `events_test.go`. In `cmd/api/main.go`, remove the broker, `Listen` and `RegisterOnShutdown`.
4. In `internal/jobs/cleanup.go`, remove the deletion of the notifications and of the real-time messages, with their constants (`notificationRetention` and `realtimeMessageRetention`) and their lines in the `CleanupArgs` comment. In `cleanup_test.go`, delete `TestCleanupDeletesOldNotifications` and `TestCleanupDeletesOldRealtimeMessages`.
5. In `internal/testutil`, delete `events.go` and `WithBroker` (in `http.go`). In the tests of other packages:
   - delete the ones that open a stream with `OpenEvents`: `TestRevokeSessionCutsItsStream` and `TestDeleteAccountCutsStreams` in `internal/auth`, and `TestEventsEndToEnd` in `internal/server`;
   - in `internal/auth`, `TestDeleteAccount` and `TestChangeEmail` also check the `notifications` table: remove those parts.
6. `make sqlc` (it also deletes the generated code of the queries you removed) and `make check`.

To remove only the notifications and keep real time:
1. Delete `internal/notifications`, the `00009_create_notifications.sql` migration and the `notifications.sql` query.
2. In `internal/server/routes.go`, remove the notifications service, `notifications.Register` and the `Notifier` of the auth options.
3. In `internal/jobs/cleanup.go`, remove the deletion of the notifications, `notificationRetention` and its line in the `CleanupArgs` comment; in `cleanup_test.go`, delete `TestCleanupDeletesOldNotifications`.
4. In `internal/auth`, remove the parts of `TestDeleteAccount` and `TestChangeEmail` that check the `notifications` table.
5. `make sqlc` and `make check`.

Both recipes were tested on a copy of the project, with a test database created only with the remaining migrations.
