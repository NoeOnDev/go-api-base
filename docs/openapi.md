# The contract of the API (OpenAPI)

`internal/openapi/openapi.yaml` describes every route of the API in a format programs read: its parameters, the body it takes, and what it answers with each status. It is [OpenAPI 3.0](https://spec.openapis.org/oas/v3.0.3), and the API serves it at `GET /openapi.yaml`.

It is for three things:

- **The frontend's types.** A generator turns it into TypeScript, so a request or a response written wrong does not compile.
- **A coding agent on the frontend,** which reads the contract instead of looking for the Go handlers.
- **Whoever integrates with the API,** who opens it in the tool they already use.

The document is written by hand, and the tests keep it true (see "How it stays true").

## From the frontend (React)

Generate the types from the API running on your machine, or from the file:

```bash
npx openapi-typescript http://localhost:8080/openapi.yaml -o src/api.d.ts
```

Each route is there with its parameters, its body and its responses. With `openapi-fetch`, which is from the same project, the requests are checked against them:

```ts
import createClient from "openapi-fetch";
import type { paths } from "./api";

const api = createClient<paths>({ baseUrl: "/api", credentials: "include" });

const { data, error } = await api.GET("/notes/{id}", { params: { path: { id: 12 } } });
// data is a Note, or undefined; error is {error: {code, message, fields?}}
```

Run the generator again when the API changes, and commit its output: a field that was renamed shows as a compile error where it was used. `baseUrl` is where the API is: `/api` with the deployment of [deploy.md](deploy.md), which puts it under that prefix of the same domain.

To read it as documentation, open `http://localhost:8080/openapi.yaml` in any OpenAPI viewer (Swagger Editor, Redocly, Scalar), or import it into an API client (Postman, Bruno, Insomnia).

## What the document says

- **Each operation has an `operationId`** (`listNotes`, `bookAppointment`), a tag that groups it, and who can call it at the start of its description: public, with a session, administrators only, or with a permission.
- **The statuses a frontend handles one by one are listed in each operation:** the `2xx`, and `401`, `403`, `404`, `409` and `422` where they can happen. Its `default` response is for the ones any route can answer: `400`, `429`, `500`.
- **Every error has the same shape,** the `Error` schema. Its `code` is one of the table of [i18n.md](i18n.md#error-codes); the description of an operation names the ones that are its own.
- **The objects are closed** (`additionalProperties: false`): a response has the fields the document says and no other, and a request body with a field the API does not know is rejected (`400 unknown_field`).
- **A date without a zone is a `string`** with its format: `date` for a day, `HH:MM` for an hour of the clock, and `2026-11-05T10:00` for a day and an hour of a place (see [time.md](time.md)). An instant is a `date-time`, in UTC.

## How it stays true

A document written by hand goes out of date, which is why the template did not have one. Two checks of `make check` prevent it:

| Check | What fails |
|---|---|
| The routes (`internal/openapi/openapi_test.go`) | A route of the code that the document does not have, or one of the document that is not in the code. Also an operation without `operationId`, summary or `default` response |
| The responses (`internal/testutil/contract.go`) | Every request a test sends with `testutil.Do` or `testutil.Upload` to the API of `testutil.NewAPI` is checked: the status must be one the operation lists, and the body must have the shape it says. If the API accepted the request (`2xx`), the request is checked too: its parameters and its body |

So the tests a resource already needs are what keeps its part of the document true. A handler that returns a new field fails with `property "x" is unsupported`; one that answers a new error, with `answered 409, which openapi.yaml does not list for it`; a test that sends a field the document does not have, with `the request is not what openapi.yaml describes`.

What they do not check is a route no test calls: the document can say anything about it. A resource with the tests `AGENTS.md` asks for (the happy path, `422`, `404`, `401`) has its part covered.

## Changing the API

Change the document in the same commit as the code:

- **A new route:** its operation under `paths`, with an `operationId`, a tag, a summary, who can call it, its parameters, its body and its responses. Copy one that looks like it: `/notes` for a resource of each user, `/products` for a list with filters.
- **A new field in a response:** in its schema, under `components.schemas`, and in `required` if it always comes.
- **A new error:** its status in the operation, if it is one of `401`, `403`, `404`, `409` or `422`, and its code in the description.
- **A new resource:** its schema, and its routes.

Then run the tests of the resource: they say what the document still has wrong.

## What it does not include

- **Generating the document from the code.** It would need the routes registered through a layer that knows the types of each handler, which is what a framework does. The handlers here are plain `net/http`, and the tests give the same guarantee.
- **A page with the documentation,** served by the API. Any viewer opens the document, and a page of the API would bring scripts from somewhere else.
- **Checking the requests against the document in production.** Each handler validates its own input, with messages in the user's language.

## Removing it

1. Delete `internal/openapi` and `internal/testutil/contract.go`.
2. In `internal/server/routes.go`, remove the `GET /openapi.yaml` route. In `internal/server/routes_test.go`, delete `TestOpenAPIDocument` (`TestRolesMatchTheDocument` can stay: it still checks the statuses).
3. In `internal/testutil/http.go`, `NewAPI` returns `server.NewHandler(d)` as it is, and the two calls to `checkContract` go.
4. `go mod tidy`, which removes `kin-openapi`, and `make check`.

This recipe was tested on a copy of the project.
