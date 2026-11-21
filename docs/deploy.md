# Deploying

A reference deployment for a single server (a VPS): the API, PostgreSQL and Caddy, which gives it HTTPS, with Docker Compose. It is a starting point that works, not the only way: the API is a single binary, and "Without Docker" below runs it with systemd.

| File | What it is |
|---|---|
| `Dockerfile` | The image of the API: the static binary on an image without a shell, as a user without privileges |
| `deploy/compose.yaml` | The three services, their volumes and their private network |
| `deploy/Caddyfile` | The proxy: HTTPS, the API under `/api` and the frontend at `/` |
| `deploy/.env.example` | The configuration of the server, to copy to `deploy/.env` |

It already takes care of several points of "Before production" in the [README](../README.md#before-production):

- `APP_ENV=production`, so the cookies are `Secure`.
- HTTPS in front, with the certificate obtained and renewed by Caddy.
- `TRUST_PROXY=true`, with the API's port closed to the internet: only Caddy is published.
- The stop waits longer than `SHUTDOWN_TIMEOUT`, so the requests and jobs in progress finish.
- A direct connection to PostgreSQL, which real time and the queue need.

The rest of that list is still yours: the emails, `APP_NAME`, whether registration stays open, and the backups.

## The first deploy

You need a server with Docker (with its Compose plugin) and a domain whose DNS already points to it. Ports 80 and 443 must be open: Caddy uses both to get the certificate.

1. **Get the code on the server:**

   ```bash
   git clone <your repository> app && cd app/deploy
   ```

2. **Write the configuration:**

   ```bash
   cp .env.example .env
   ```

   Fill it in: the domain (`SITE_ADDRESS`), the address of the frontend (`FRONTEND_URL`), the SMTP details, and two secrets. Generate and write down the first one before the second: Compose does not run without `POSTGRES_PASSWORD`. The second command builds the image the first time, so the key is the last line it prints.

   ```bash
   openssl rand -hex 24                                # POSTGRES_PASSWORD
   docker compose run --rm --no-deps api generate-key  # ENCRYPTION_KEY (it builds the image first)
   ```

   Keep a copy of `.env` apart from the server. If `ENCRYPTION_KEY` is lost, the users have to set up 2FA again (see [encryption.md](encryption.md)).

3. **Start it:**

   ```bash
   docker compose up -d --build
   ```

   The API applies the migrations on startup. `docker compose ps` shows the three services, and the API as `healthy` once it answers.

4. **Create the administrator.** The command asks for the password in your terminal:

   ```bash
   docker compose exec api /api create-admin -email you@example.com -name "Your Name"
   ```

   With `REQUIRE_ADMIN_TWO_FACTOR=true`, which `.env.example` brings, that account acts as an administrator once it turns on 2FA from the frontend; the command says so.

5. **Check it:** `https://<your domain>/api/healthz` answers `{"status":"ok"}`.

`make seed` and `make fresh` are for development: the binary refuses to run them in production.

## The frontend

The Caddyfile serves the frontend and the API under the same domain, which is what the template recommends: the session cookie works without CORS (see "From the frontend" in [auth.md](auth.md#from-the-frontend-react)).

| Address | What answers |
|---|---|
| `https://example.com/api/…` | The API. The `/api` prefix is removed before the request reaches it: `/api/auth/login` arrives as `/auth/login` |
| `https://example.com/…` | The files of `deploy/site`: the build of the React app |

Build the frontend with `/api` as the base of its requests, and copy the result:

```bash
npm run build && rsync -a --delete dist/ server:app/deploy/site/
```

Caddy serves the new files at once: there is nothing to restart. A path that is not a file gets `index.html`, so the routes of the app work when the page is reloaded.

The paths the API returns (`"url": "/files/12"`, the `Location` header) are the API's own: the frontend puts its base in front, as with any other request.

**With the frontend somewhere else** (another server, a static hosting): the Caddyfile has a second block, commented, that serves only the API on its own subdomain. Use it instead of the first one, and set `CORS_ALLOWED_ORIGINS` to the origin of the frontend.

## Updating

```bash
git pull && docker compose up -d --build
```

It builds the new image and replaces the API's container. The stop is graceful: the API finishes the requests in progress, and the queued jobs stay in the database for the new one. While it restarts, Caddy holds the new requests up to 20 seconds (`lb_try_duration`) instead of answering an error, so a deploy looks like a slow request, not like a failure: with the API stopped for 6 seconds, every request sent meanwhile was answered, the slowest after those 6 seconds. The real-time streams are cut and the browsers open them again on their own.

The new version applies its migrations on startup. A migration that the old code cannot live with (dropping a column it reads) needs two deploys: first the code that stops using it, then the migration.

**To go back,** check out the previous commit and run the same command. It brings the code back, not the schema: a migration already applied stays.

**On a small server** (1 GB of memory), building the image can run out of memory. Build it somewhere else, push it to a registry, and set `API_IMAGE` in `.env` to pull it instead:

```bash
docker build -t registry.example.com/app:1.4.0 . && docker push registry.example.com/app:1.4.0
# on the server, with API_IMAGE=registry.example.com/app:1.4.0 in .env:
docker compose pull api && docker compose up -d
```

## Logs

```bash
docker compose logs -f api           # the API, in JSON: one line per request, with its request_id
docker compose logs -f --since 10m   # the three services
```

Docker keeps the logs of a container without a limit unless it is told otherwise. Set one for the whole server in `/etc/docker/daemon.json` (`{"log-driver": "local"}` rotates them), or send them to where your other logs go.

## Backups

Two things hold data: the database, and the uploaded files if they are on this server (`STORAGE_DRIVER=local`).

```bash
# The database, as SQL. Run it from a cron of the server, and copy the file somewhere else.
docker compose exec -T db pg_dump -U app app | gzip > backup-$(date +%F).sql.gz

# The uploaded files.
docker run --rm -v deploy_storage:/data:ro -v "$PWD":/out alpine tar czf /out/storage-$(date +%F).tar.gz -C /data .
```

To restore the database on an empty server, start only `db` and load the file before starting the API:

```bash
docker compose up -d db
gunzip -c backup-2026-11-21.sql.gz | docker compose exec -T db psql -U app app
docker compose up -d
```

With `STORAGE_DRIVER=s3` the files are in the bucket, which is backed up apart, and the server only has the database. And `.env`: without `ENCRYPTION_KEY`, a restored database cannot read its 2FA secrets.

A backup that was never restored is a guess: try the restore once, on another machine.

## Trying it on your machine

The same files run locally, with a certificate of Caddy's own:

```bash
cd deploy && cp .env.example .env
# in .env: SITE_ADDRESS=localhost, FRONTEND_URL=https://localhost, MAIL_DRIVER=log, and the two secrets
docker compose up -d --build
curl -k https://localhost/api/healthz
```

`-k` accepts that certificate, which a browser will warn about. If ports 80 and 443 are taken on your machine, set `HTTP_PORT=8080` and `HTTPS_PORT=8443` in `.env`, and the address is `https://localhost:8443`. `docker compose down -v` stops everything and deletes its volumes.

## More than one server

This deployment is one server. The API itself runs with several instances (see "Multiple instances" in [decisions.md](decisions.md#multiple-instances)), and what changes around it is:

- **The database** goes to its own server, or to a managed service, and `DATABASE_URL` points to it. Remove the `db` service.
- **The uploaded files** go to a bucket (`STORAGE_DRIVER=s3`): each server would have its own disk.
- **A load balancer** in front of the servers, instead of one Caddy per server getting certificates. Set `TRUSTED_PROXIES` to its address.

## Without Docker

The API is a static binary with everything inside (migrations, texts, email templates, time zones). `make build` leaves it in `bin/api`; copy it to the server with its environment file, and run it with systemd:

```ini
# /etc/systemd/system/api.service
[Unit]
Description=API
After=network-online.target postgresql.service

[Service]
User=api
EnvironmentFile=/etc/api/env
ExecStart=/usr/local/bin/api
Restart=always
# More than SHUTDOWN_TIMEOUT (10s), so the requests and the jobs in progress finish.
TimeoutStopSec=30
# The process cannot write outside the directory of the uploaded files.
ProtectSystem=strict
ReadWritePaths=/var/lib/api
NoNewPrivileges=true

[Install]
WantedBy=multi-user.target
```

In `/etc/api/env`: `APP_ENV=production`, `HTTP_ADDR=127.0.0.1:8080` (only the proxy of the same server reaches it), `TRUST_PROXY=true`, `TRUSTED_PROXIES=127.0.0.1`, `STORAGE_DIR=/var/lib/api/storage`, `DATABASE_URL` and the rest. In front, Caddy installed on the server with the same Caddyfile, changing `api:8080` to `127.0.0.1:8080` and `/srv/site` to where the frontend is.

## What it does not include

- **Kubernetes manifests, or the files of a cloud platform.** The image is a standard one: it listens on `HTTP_ADDR`, reads its configuration from the environment, has a health check (`GET /healthz`, or `/api healthcheck` inside the container) and stops in order on `SIGTERM`.
- **A pipeline that deploys on each push.** The CI builds the image to check that the Dockerfile works; where to send it depends on each project.
- **Automatic backups.** The commands are above; the schedule and where to keep them are yours.
- **Monitoring.** `GET /healthz` is what an uptime monitor asks.

## Removing it

Delete `Dockerfile`, `.dockerignore`, the `deploy` directory and the `image` job of `.github/workflows/ci.yml`. The `healthcheck` command (`cmd/api/health.go`, its case in `main.go` and `TestHealthcheck`) can stay: it only asks `/healthz`.
