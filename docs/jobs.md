# Trabajos en segundo plano

La plantilla usa [River](https://riverqueue.com), una cola de trabajos guardada en **PostgreSQL**: no hace falta Redis. Es el equivalente a las colas de Laravel (`queue:work`, `schedule:run`):

- **Los trabajos sobreviven a un reinicio.** Si la API se cae o se reinicia, los pendientes siguen en la base.
- **Se reintentan si fallan**, con una espera cada vez más larga.
- **Se pueden programar** para más tarde, o repetir cada cierto tiempo.
- **Se pueden encolar dentro de una transacción**, junto con los datos: se guardan los dos o ninguno.

**Los workers corren dentro del mismo proceso que la API.** En Laravel hace falta otro proceso (`queue:work`), porque cada petición de PHP vive y muere sola; en Go, la API ya es un proceso que vive siempre. Así sigue siendo un solo binario y un solo servicio que desplegar.

## Lo que ya usa la cola

| Trabajo | Cola | Qué hace |
|---|---|---|
| `send_email` | `email` | Envía los correos. Hasta 5 intentos, con el contenido cifrado (ver abajo) |
| `cleanup` | `default` | Cada hora borra las sesiones y los tokens vencidos, las notificaciones leídas hace más de 90 días, las claves de los límites de intentos que ya se recargaron y los mensajes grandes del tiempo real |

Hay dos colas, cada una con su propio límite de workers, así un trabajo lento en una no frena a la otra:

- **`default`:** `JOBS_MAX_WORKERS` trabajos a la vez (10 por defecto).
- **`email`:** 4 a la vez, porque cada envío abre una conexión SMTP y los proveedores rechazan muchas simultáneas.

Los workers usan el mismo pool de conexiones que la API, que tiene 10 si `DATABASE_URL` no indica otro tamaño (pgx usaría solo 4). Si subís `JOBS_MAX_WORKERS`, conviene subir también el pool (`?pool_max_conns=20`).

## Crear un trabajo

Un trabajo tiene dos partes: los **argumentos**, que se guardan en la base como JSON, y el **worker**, que lo ejecuta.

```go
// internal/jobs/report.go
package jobs

// ReportArgs es el trabajo de generar el reporte mensual de un usuario.
type ReportArgs struct {
	UserID int64 `json:"user_id"`
	Month  string `json:"month"` // "2026-10"
}

// Kind es el nombre del trabajo en la base. No lo cambies después de desplegar:
// los trabajos pendientes con el nombre viejo no encontrarían su worker.
func (ReportArgs) Kind() string { return "monthly_report" }

type reportWorker struct {
	river.WorkerDefaults[ReportArgs]
	q *store.Queries
}

func (w *reportWorker) Work(ctx context.Context, job *river.Job[ReportArgs]) error {
	// Devolver un error reintenta el trabajo más tarde.
	return generateReport(ctx, w.q, job.Args.UserID, job.Args.Month)
}
```

Después, registralo en `registerWorkers` (`internal/jobs/workers.go`), igual que las rutas en `server/routes.go`:

```go
if err := river.AddWorkerSafely(workers, &reportWorker{q: deps.Queries}); err != nil {
	return err
}
```

Si el worker necesita algo más (el broker de tiempo real, un cliente HTTP), sumalo a `jobs.Deps` y pasalo desde `cmd/api/main.go`.

## Encolar

Desde un handler, con el cliente de `server.Deps.Jobs` (pasáselo al recurso al registrarlo en `routes.go`):

```go
if _, err := h.jobs.Insert(r.Context(), jobs.ReportArgs{UserID: user.ID, Month: "2026-10"}, nil); err != nil {
	return fmt.Errorf("encolar reporte: %w", err)
}
```

**Dentro de una transacción**, para que el trabajo exista solo si los datos se guardaron:

```go
tx, err := h.db.Begin(r.Context())
if err != nil {
	return err
}
defer tx.Rollback(r.Context())

order, err := h.q.WithTx(tx).CreateOrder(r.Context(), params)
if err != nil {
	return err
}
// Si algo falla después y se hace Rollback, el trabajo tampoco queda encolado.
if _, err := h.jobs.InsertTx(r.Context(), tx, jobs.OrderConfirmationArgs{OrderID: order.ID}, nil); err != nil {
	return err
}
return tx.Commit(r.Context())
```

El trabajo no se ejecuta hasta que se confirma la transacción, así que el worker siempre encuentra el pedido.

## Opciones de cada trabajo

Se definen con un método `InsertOpts` en los argumentos (aplica siempre), o al encolar (solo esa vez):

```go
func (ReportArgs) InsertOpts() river.InsertOpts {
	return river.InsertOpts{
		Queue:       river.QueueDefault,
		MaxAttempts: 10,                     // por defecto, 25
		ScheduledAt: time.Now().Add(time.Hour), // ejecutarlo más tarde
		// Uno solo por usuario y mes, aunque se encole dos veces:
		UniqueOpts: river.UniqueOpts{ByArgs: true},
	}
}
```

Dentro del worker:

- **`return err`:** se reintenta más tarde. River espera cada vez más entre intentos: segundos al principio, horas al final.
- **`return river.JobCancel(err)`:** no se reintenta, porque no serviría de nada (por ejemplo, el usuario ya no existe).
- **`return river.JobSnooze(30 * time.Second)`:** se vuelve a intentar en 30 segundos, sin contar como fallo (por ejemplo, una API externa pide esperar).

## Datos sensibles en los argumentos

Los argumentos se guardan en la tabla `river_job` sin cifrar, y River conserva los trabajos terminados 24 horas. **Si llevan un secreto o un dato personal, cifrá lo sensible** con `secretbox`, como hace `SendEmailArgs` con el correo entero: un correo de recuperación lleva un enlace con token, y sin cifrar, quien lea la tabla podría usarlo. Mejor todavía es guardar solo un id y buscar el resto en el worker.

## Trabajos periódicos

El equivalente al scheduler de Laravel. Se agregan en `periodicJobs` (`internal/jobs/workers.go`):

```go
river.NewPeriodicJob(
	river.PeriodicInterval(24*time.Hour),
	func() (river.JobArgs, *river.InsertOpts) { return DailyDigestArgs{}, nil },
	&river.PeriodicJobOpts{ID: "daily_digest", RunOnStart: true},
),
```

- Con varias instancias, solo los encola una, la que River elige como líder.
- `RunOnStart` lo encola también al arrancar. Para no repetirlo si la API se reinicia seguido, hacelo único por período (`UniqueOpts.ByPeriod`), como `CleanupArgs`.
- Para horarios fijos (todos los días a las 3:00), River acepta un horario tipo cron: ver su [documentación](https://riverqueue.com/docs/periodic-jobs).

## Al apagar

La API deja de recibir peticiones y, al mismo tiempo, los workers tienen hasta `SHUTDOWN_TIMEOUT` para terminar lo que están haciendo. Que se detengan a la vez es seguro: si una petición que todavía está terminando encola un trabajo, queda en la base y se ejecuta al arrancar.

- **Los trabajos pendientes, o que estaban esperando un reintento,** siguen en la base y se retoman al arrancar.
- **Uno que quedó cortado a mitad de camino** tampoco se pierde: River lo reintenta cuando lleva una hora "en curso". Por eso un worker tiene que poder ejecutarse dos veces sin romper nada (por ejemplo, comprobando si el reporte ya existe).

## Ver la cola

```sql
-- Lo pendiente, lo que se está reintentando y lo que falló del todo
SELECT id, kind, state, attempt, max_attempts, scheduled_at, errors[array_length(errors, 1)] AS ultimo_error
FROM river_job
WHERE state NOT IN ('completed', 'cancelled')
ORDER BY id DESC;
```

River tiene una interfaz web, [River UI](https://riverqueue.com/docs/river-ui), parecida a Horizon. No viene en la plantilla porque suma otra dependencia y necesita su propia autenticación.

## Tests

Un worker se prueba con `rivertest`, sin levantar la cola (ver `internal/jobs/email_test.go`):

```go
worker := rivertest.NewWorker(t, riverpgxv5.New(pool), &river.Config{}, &reportWorker{q: store.New(pool)})
res, err := worker.Work(t.Context(), t, tx, ReportArgs{UserID: 1, Month: "2026-10"}, nil)
```

Para comprobar que un handler encoló un trabajo, armá el cliente con `jobs.New` (sin `Start`) y usá `rivertest.RequireInserted`.

## Actualizar River

1. Actualizá `github.com/riverqueue/river` en `go.mod` y `RIVER_VERSION` en el `Makefile`, a la misma versión.
2. Revisá su [CHANGELOG](https://github.com/riverqueue/river/blob/master/CHANGELOG.md). Si la versión trae una migración nueva, creá una migración de goose con su SQL:

   ```bash
   make migrate-new name=river_vN
   make river-migration version=N
   ```

   El segundo comando imprime las secciones Up y Down, listas para pegar en la migración nueva.

## Separar los workers en otro proceso

Si un proyecto tiene trabajos pesados que no deben competir con la API por la CPU, se pueden correr aparte:

1. Un subcomando `worker` en `cmd/api` que arme el cliente con `jobs.New`, llame a `Start` y espere la señal de apagado, sin servidor HTTP.
2. En la API, armar el cliente sin `Queues` y sin llamar a `Start`: solo encola.

No viene armado, porque un solo proceso alcanza para la mayoría de los proyectos y es más simple.

## Quitar la cola

Si un proyecto no la necesita:

1. **Primero, la limpieza de cada hora**, que hoy es un trabajo de la cola. Pasá el cuerpo de `cleanupWorker.Work` (`internal/jobs/cleanup.go`), con sus constantes, a una función `cleanup(ctx, q, idle, logger) error` en `cmd/api/cleanup.go`. Al lado, escribí `cleanupEvery`, que la llama al arrancar y después cada hora con un `time.Ticker`, hasta que `ctx` se cancela. En `run`, lanzala con `go cleanupEvery(ctx, store.New(pool), cfg.SessionIdleTimeout, logger)`. Con varias instancias, cada una limpia por su cuenta, y no importa: son borrados que no chocan. Los tests de `cleanup_test.go` usan River: adaptalos para llamar a `cleanup` directo, o borralos.
2. Borrá `internal/jobs` y la migración `00008_create_river.sql`. En el `Makefile`, quitá `RIVER_VERSION`, `RIVER_CLI` y el objetivo `river-migration` (y su fila en `AGENTS.md` y en el README).
3. En `cmd/api/main.go`, quitá el cliente de la cola y usá el `mail.Sender` directo (`sender`) como `Mailer`: los correos se envían dentro de la petición, sin reintentos. `shutdown` queda solo con el servidor HTTP: quitale el parámetro de los trabajos y la interfaz `stopper`. Borrá `main_test.go`, que prueba el apagado de los trabajos.
4. En `internal/server/routes.go`, quitá el campo `Jobs` de `Deps`. En `cmd/api/keys.go`, quitá la parte de los correos encolados (`jobs.QueuedEmailKeys` y `AddQueuedEmails`).
5. En `internal/config`, quitá `JobsMaxWorkers`, sus casos en `config_test.go` y `positiveInt`, que queda sin uso. Quitá también `JOBS_MAX_WORKERS` de `.env.example` y del README.
6. `make sqlc` (el esquema de River también genera tipos en `internal/store`), `go mod tidy` y `make check`.

Esta receta se probó en una copia del proyecto, con una base de pruebas creada solo con las migraciones que quedan.
