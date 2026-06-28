# Server Architecture

_Last updated:_ 27/06/2026

## Purpose

The server is the FutureCast control plane. It owns the configuration, ingests articles from external sources, drives the classification loop against the inference container, persists the results, and exposes the API the dashboard consumes. Everything that decides _what_ what counts as a source, what gets classified, what shape the data takes lives here. The dashboard renders, the inference service classifies, the server reconciles.

The codebase is a single Go binary organized around a small set of controllers, one per concern. Controllers depend on each other through interfaces declared in the consuming package, which keeps the wiring decoupled and lets tests substitute lightweight fakes without touching production code. Source ingest is cron-driven, classification is signal-driven sources kick a level-triggered loop on the inference controller after every
successful insert, and the loop drains the article store until empty before going idle.

The entry point is [cmd/server/main.go](../../server/cmd/server/main.go).

## Stack

| Concern       | Choice                                                                                                                                                                                                    |
| ------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Language      | Go 1.25, modules-only.                                                                                                                                                                                    |
| HTTP          | `net/http` with the standard library's `http.ServeMux`.                                                                                                                                                   |
| Database      | PostgreSQL 18, accessed via `pgx/v5` connection pool.                                                                                                                                                     |
| Config        | YAML loaded by `spf13/viper`, hot-reload via `ConfigManager`.                                                                                                                                             |
| Scheduler     | `robfig/cron/v3` with a labeled job registry on top.                                                                                                                                                      |
| Outbound HTTP | `pkg/httpx` shared client with retry, auth, headers, user-agent.                                                                                                                                          |
| Inference     | `pkg/inference.Client` dispatches per article, either to a self-hosted Python container or directly to any OpenAI-compatible remote endpoint (OpenRouter, OpenAI, vLLM, …) depending on `inference.type`. |
| Logging       | `log/slog` with a custom broadcaster that fans out over SSE.                                                                                                                                              |
| Tests         | stdlib `testing` + `google/go-cmp`, integration suite gated by tag.                                                                                                                                       |

## Project Layout

```
server/
  cmd/server/           Binary entrypoint, wiring composition
  api/v1/               Public request/response types shared with the client
  pkg/
    config/             ConfigManager, YAML parsing, filter DSL
    controller/         One package per concern (system, scheduler, ...)
    httpx/              Shared outbound HTTP client (retry, auth, headers)
    inference/          Client (Python or remote API), Mode (continuous/manual), Container (compose lifecycle)
    logger/             slog handler + broadcaster for live log streaming
    registry/           Postgres stores grouped by resource
    scheduler/          robfig/cron wrapper with labeled job registry
    schema/             Schema-mutation guard (RWMutex)
    server/             HTTP router, middleware, JSON helpers
    source/             Source driver interface + per-type drivers
    utils/              Crypto, id, time helpers
```

Controllers own workflow decisions. Stores own SQL. API handlers only decode
requests, call a controller, and encode the response. Anything that depends
on a sibling controller takes it as an interface declared in the consuming
package, concrete `*Controller` types satisfy those interfaces structurally,
so there is no factory layer.

## Startup

Boot is a strict sequence. The config controller is initialized first because
every subsequent step needs to consult the parsed YAML the logger needs the
log level, the database connection needs the DSN, and the controllers all
need their own config-derived settings. If the config fails to parse, the
process exits before opening any external resources.

With the logger in place, the server opens a Postgres connection pool. It
prefers the DSN explicitly set in the config, but falls back to the
`DATABASE_URL` environment variable when that field is blank. This makes local
development against the compose stack work without ceremony.

Immediately after the pool is reachable, the server runs
`registry.EnsureBaseSchema(ctx)`. The base schema (sources,
source_urls, articles, views, monitor_uptime, plus the
`upsert_monitor_uptime` PL/pgSQL function) is embedded into the
binary via `//go:embed base.sql` and applied with `CREATE … IF NOT
EXISTS` + DO-block ENUM checks, so the call is idempotent and safe to
re-run against an already-initialised database. This is what allows
the compose stack to drop the old `docker-entrypoint-initdb.d` mount
and what lets managed Postgres (RDS, Supabase, Neon, …) work without
a manual migration step.

The store layer is constructed next. Each store wraps the shared pool with a
resource-scoped logger and exposes a typed surface for one table family. The
stores hold no state of their own and are cheap to build.

The inference subsystem follows: a `Client` that dispatches classify
requests (to the Python service for `type: zeroshot`/`llm` or directly
to the configured remote endpoint for `type: api`), a `Container`
helper that manages the Python compose service when one is in use,
and a `Mode` strategy that decides where articles come from
(`ContinuousMode` drains the unprocessed-articles table, `ManualMode`
accepts caller-supplied articles via the classify-inline route). A
single schema guard is created at the same time every controller
that touches the classification tables or the articles table receives
the same pointer so they all serialize against the same writer.

The controllers are then wired in dependency order: inference, then source,
then classification, then view, then scheduler, then system. Once everything
is connected, the system controller's startup method reconciles the database
against the config. It brings the source list in sync, creates or drops
classification tables to match, schedules per-source ingest jobs, and starts
the heartbeat that records uptime.

Finally the HTTP server starts listening on the address from the config.
Shutdown is driven by `SIGINT` or `SIGTERM`, the signal context propagates
down through every controller's long-lived context parameter.

## Configuration

The server reads a single YAML file at startup. That file declares everything
needed to operate: bind address, database DSN, log level, sources to ingest
from, and classifications to run against incoming articles. The runtime keeps
an in-memory copy of the parsed config and exposes a read-only view to every
component that needs to consult it. Reads are wait-free because the manager
swaps the snapshot atomically, writers and readers never block each other.

When the dashboard saves a new config, the server writes the file back to
disk and re-parses it. Subscribers receive a callback so they can react. The
inference controller, for example, compares the orchestration-relevant fields
against what was previously applied and only restarts the inference container
when one of those changed. Edits that only touch labels or thresholds are
absorbed by the live spec on the next classify call without cycling the
container.

The implementation lives at [pkg/config](../../server/pkg/config/config.go),
the controller that wraps it lives at
[pkg/controller/config](../../server/pkg/controller/config/controller.go).

> [!NOTE]
> Complete field reference for the YAML schema is in
> [config.md](../reference/config.md).

## Schema Guard

The schema guard exists to prevent a specific Postgres deadlock that surfaces
during config reloads. The mechanism lives at
[pkg/schema](../../server/pkg/schema/guard.go).

Each classification table has a foreign key from its `article_id` column back
to the articles table. When the system controller drops a classification
table because the user removed it from the config Postgres needs to remove
the FK trigger, and removing the trigger requires an `AccessExclusiveLock` on
the parent articles table. At the same moment, the classifier might be
inserting a batch of results into another classification table, which takes a
`RowExclusiveLock` on articles for foreign-key validation. The two locks
block each other, the lock graph forms a cycle, and Postgres aborts one
transaction with `SQLSTATE 40P01`.

The guard is a single read-write mutex shared across the controllers that
touch these tables. Schema mutations take the write lock, anything that reads
from or writes to article or classification tables takes the read lock.
Because the guard is a Go-level mutex, it serializes within the process
before queries even reach the database, so the foreign-key cycle never has
a chance to form.

The scheduler cooperates with the guard. Its stop method waits for in-flight
cron jobs to drain rather than returning as soon as the cron loop is
signaled. Together these two mechanisms cover the full surface: scheduled
work drains during stop, ad-hoc API calls and the webhook listener take the
guard explicitly, and the schema mutator holds it exclusively while it runs.

The guard only protects in-process callers. When integration tests run
against the same database as a live server, the cross-process race still
exists, the test harness handles that case by retrying on deadlock errors.

## Controllers

The codebase is organized around a small set of controllers. They are
constructed once at startup and never recreated. Each controller declares its
dependencies as interfaces in its own package, which means tests can
substitute lightweight fakes and the production wiring stays decoupled.

**Config**

The config controller wraps the underlying `ConfigManager` and exposes the
small surface other controllers need: read the live config, subscribe to
reload events, persist a new YAML body, and produce the client-facing payload
that the dashboard's config editor renders.

**System**

The system controller is the orchestrator. It owns the boot sequence and the
response to configuration changes. When the dashboard saves a new config, the
system controller stops the scheduler, waits for in-flight jobs to drain, and
then runs the same startup sequence again. The result is that a config change
is a controlled restart, not a hot-patch.

The most interesting work happens in the two sync methods. The source sync
compares the source rows currently in the database with the sources resolved
from the config. Sources whose URL is new or whose content hash has changed
get re-upserted, sources that no longer appear in the config get deleted.
The hash comparison is what makes this idempotent running the sync twice
with the same config is a no-op.

The table sync does the same kind of diff but for classification tables.
Each classification declared in the config has a dedicated table plus a
metrics companion. Tables that no longer match a configured classification
get dropped, new classifications get tables created, existing ones whose
attribute set has drifted get a bulk JSON key update that adds missing
fields and removes orphaned ones. The whole sync loop runs while holding the
schema guard exclusively.

The heartbeat is a 15-second ticker that calls a PL/pgSQL function on the
database. The function either extends the current uptime window or starts a
new one, depending on how long it has been since the last beat. The
dashboard reads from the resulting rows to render uptime graphs.

The implementation lives at
[pkg/controller/system](../../server/pkg/controller/system/controller.go).

**Scheduler**

The scheduler controller is a thin layer over `robfig/cron/v3`. It
exposes Add / Remove / Run / Stop and does nothing else there is
no persisted jobs table and no runtime endpoint to upsert entries.
A status surface (replacing the old `NextJob` list) will land here
when the dashboard's active-jobs view is designed.

All cadence comes from the config. The system controller's
`loadSources` step iterates the active source list and registers one
cron entry per retriever (RSS or HTTP), labelled `<type>:<id>` with
the source's own `schedule` field as the expression. Webhook sources
are not scheduled, they hang off the HTTP mux and push articles into
the source controller's listener channel as they arrive.

The classification loop is not cron-driven anymore. The inference
controller owns a level-triggered worker that fires whenever a source
inserts an article. See the Inference section below for the loop's
shape.

The implementation lives at
[pkg/controller/scheduler](../../server/pkg/controller/scheduler/controller.go).

**Source**

The source controller owns the per-type driver registry. Each source kind
RSS, HTTP, webhook is implemented as a driver under
[pkg/source](../../server/pkg/source/driver.go). The controller registers
drivers lazily when it sees the first active source of that type, so a
server with no webhook sources never instantiates the webhook listener.

The controller dispatches scheduled fetches into the appropriate driver,
runs a background drainer for any listener-style driver, and exposes the
manual "run now" endpoints the dashboard uses to trigger an immediate
fetch. Every path that writes to the articles table or reads from it does
so while holding the schema guard as a reader. This includes the
cron-driven fetch, the webhook drainer, the manual run-now API, and the
dashboard's recent-articles and rate endpoints. The cost is negligible
because the guard is only ever contended during schema changes.

After every successful article insert the controller calls `Kick()`
on the inference controller. Kick is mode-aware: in continuous mode
it spawns the drain loop if one is not already running, in manual
mode it is a no-op. This is what makes ingest signal-driven and lets
the worker exit cleanly when the article store empties.

Per-source filters apply before any insert. The system controller
parses `source.filter` (default + per-source override) at config
load, pushes the resulting `map[url][]Filter` into the source
controller via `SetFilters`, and the source controller drops failing
articles before they reach the article store. A malformed filter
fails the load with a clear error rather than surfacing at first
article.

Per-source policy how often to poll, how long to wait for a response
is resolved by two helpers. If the source spec sets an explicit value, it
wins, otherwise the controller falls back to a per-type default. RSS
defaults to every ten minutes, HTTP defaults to every five.

The implementation lives at
[pkg/controller/source](../../server/pkg/controller/source/controller.go).

**Classification**

The classification controller serves the dashboard's read endpoints
over the classification tables plus the bulk-upload path. The classify
loop itself lives in the inference controller sources kick that
worker after every successful insert, and the worker self-terminates
when there is no more unprocessed work.

The read endpoints search, count, metrics, heatmap, treemap, plot,
scatter, and quadrant each forward a query to the classification
store and shape the result for the dashboard. The scatter endpoint is
slightly more involved because it attaches a deterministic axis index
to each label, computed from the global label map maintained by the
config.

There is also an upload endpoint that accepts a JSON batch of
pre-computed classifications. It groups them by classification name
and inserts each group as a batch. This is the path for offline
workflows where the Python service is not in the loop.

The implementation lives at
[pkg/controller/classification](../../server/pkg/controller/classification/controller.go).

**Inference**

The inference controller composes three building blocks from
`pkg/inference`: a `Client` (wire-level transport Python service for
`type: zeroshot`/`llm`, OpenAI-compatible remote for `type: api`), a
`Container` helper for the docker compose lifecycle, and a `Mode`
strategy (`ContinuousMode` for the steady-state DB-draining loop,
`ManualMode` for caller-driven one-shots). The level-triggered event
loop lives on the controller itself, not on the client the client
holds no queue or running state.

At construction time the controller subscribes to config reload
events. The classification spec sent with each classify call is
pulled from the live config on every request, so a reload that only
touches labels takes effect on the next call without restarting
anything. The reload subscriber compares the previous and new configs
for inference-relevant fields only `type`, `model`, `engine`. When
one of those changes the controller flips `ready=false`, calls
`syncToConfig` (which updates the dispatch target, and for
self-hosted modes POSTs `/load` + waits for `/health` to flip live),
then `ready=true` and fires Kick once to drain any backlog. Label-only
edits skip the whole dance.

`Kick()` is mode-aware. When `Mode.AutoDrive()` is true (continuous)
it spawns the loop if one is not already running, subsequent kicks
while the loop is alive are no-ops. When false (manual) Kick is a
no-op the route handler calls `ClassifyInline(ctx, articles)`
directly, which runs the client per article and persists results via
the Mode. Source `Kick` calls during manual mode are silently
ignored.

The loop body is: drain whatever is in the in-memory queue
article-by-article, calling `client.Classify` per article and
accumulating results, once the queue empties, call `mode.Persist` on
the accumulated batch and `mode.Refill` for the next batch, loop
until refill returns empty. The exit flips `isRunning` back to false,
the next Kick spins the loop up again.

For `type: api` there is no Python container to wait on. `syncToConfig`
skips the `/health` probe entirely and flips ready as soon as
`SetTarget` completes failures surface as classify errors per
article and the next Kick retries naturally. `Container.Stop` is
still wired so deployments transitioning from self-hosted to api can
bring the now-unused Python container down.

The implementation lives at
[pkg/controller/inference](../../server/pkg/controller/inference/controller.go).

**View**

The view controller stores and renders dashboard views. A view is a
slug-addressed page composed of panels, where each panel carries a
declarative query that the controller resolves to a visualization envelope
at request time. The frontend does not consume this surface yet, so the
controller is implemented but lightly exercised.

## Scheduler

The core scheduler under
[pkg/scheduler](../../server/pkg/scheduler/cron.go) is a thin wrapper over
`robfig/cron/v3`. It adds a labeled job registry so callers can refer to
entries by human-readable names instead of cron IDs.

Adding a job validates the expression as a side effect of registration: if
the parser rejects the expression, the call returns an error and the entry
is never created. Removing a job drops the registration, subsequent ticks
will not fire it.

The stop method is worth special attention. The underlying cron package
returns a context that closes when all in-flight jobs have finished, this
wrapper blocks on that context rather than returning immediately. The
system controller relies on this guarantee. When it calls restart, it
expects that by the time stop returns, no scheduled work is still running.
Without this guarantee the schema guard would have to gate every scheduled
function explicitly, which would thread the guard through more of the
codebase than necessary.

## Source

Source drivers live under [pkg/source](../../server/pkg/source/driver.go).
Each driver is responsible for turning external content into a stream of
articles that the source controller can store. The driver interface comes
in two flavors depending on whether the driver pulls or receives content.

A _retriever_ exposes a fetch method that the scheduler calls periodically.
It returns a slice of articles synchronously, concurrency is the caller's
concern, not the driver's. RSS and HTTP are both retrievers.

A _listener_ exposes start, stop, register, and deregister methods. The
listener runs as a long-lived component, typically registering an HTTP
handler at startup. When it observes new content it pushes an article into
a channel that the source controller drains in a background goroutine.
Webhook is currently the only listener.

**Rss**

The RSS driver at [pkg/source/rss](../../server/pkg/source/rss/rss.go)
wraps the `gofeed` parser. Each fetch parses the feed at the source URL,
walks the items, and turns each one into an article. HTML in titles and
descriptions is stripped and whitespace is collapsed before storage so the
dashboard does not have to render arbitrary markup.

Each source can pick which feed field becomes the article content: the
item description, the full content payload, or the linked page. The link
option is a placeholder for a future page-scrape driver, it currently
produces zero articles.

**Http**

The HTTP driver at [pkg/source/http](../../server/pkg/source/http/http.go)
issues one request per scheduler tick using the shared `httpx` client,
which centralizes the user-agent, timeout, retry, and authentication
policy across every outbound call. The request method, query parameters,
body, and pagination cursor all come from the source's HTTP spec.

The response body is decoded as JSON and walked using the source's
`extract` paths `items` picks the array of records out of the
payload (`"$"` or `""` means the body itself is the array), then per-
record `title`, `content`, `timestamp`, and `link` dot-paths populate
each article. Missing extract config or a body that's not an array
where `items` points fails the fetch with a clear error.

Pagination is parsed from the spec but not yet executed multi-page
APIs return only the first page today.

**Webhook**

The webhook driver at
[pkg/source/webhook](../../server/pkg/source/webhook/webhook.go) implements
an HTTP handler that the top-level server mounts at `/webhooks/`. Each
active webhook source registers its path at startup, the registration
strips a leading `/` so config can write either `alerts` or `/alerts`
without breaking the dispatch.

The handler reads the body once, runs HMAC verification when the
source's `auth.kind` is `hmac` (constant-time compare of
`hex(HMAC-SHA256(secret, body))` against the configured header,
missing header → 401, mismatch → 403), decodes the body as JSON,
extracts the title, content, timestamp, and link fields using the
dot-paths from the source's `extract` config, builds an article, and
pushes it onto the listener channel. Requests with no body, malformed
JSON, missing signature, wrong signature, or no extractable content
return a `400`/`401`/`403` so the upstream caller knows what to fix.

**File (upcoming)**

A file-system retriever is planned for offline corpora and bulk loads. It
will reuse the existing retriever interface and slot into the driver
registry alongside RSS and HTTP. It is not yet implemented.

## Inference

The inference subsystem on the Go side is composed of three building
blocks in [pkg/inference](../../server/pkg/inference/):

- **`Client`** wire-level transport. Holds a `Target` (`Type`,
  `Addr`, `Endpoint`, `APIKey`, `Model`) updated atomically by
  `SetTarget`. The single `Classify(ctx, article, spec)` entry point
  dispatches on `Target.Type`: `zeroshot` / `llm` POST the whole spec
  to the Python service at `Addr/classify`, `api` calls the OpenAI
  chat-completions path one request per attribute against
  `Endpoint/chat/completions`. The client also exposes `Info` and
  `Load` for the self-hosted readiness gate.
- **`Mode`** article-fetching strategy. `ContinuousMode` refills
  from the unprocessed-articles table and persists results +
  marks-processed under the schema guard's read lock. `ManualMode`
  has a no-op refill and a persist that writes rows without touching
  the articles table (manual articles aren't necessarily DB rows).
  `Mode.AutoDrive()` tells the controller whether `Kick` should
  spawn the loop.
- **`Container`** docker compose helper for the Python service.
  Exposes `Stop` (for transitioning to api mode) and `WaitForHealth`
  (poll `/health` until `200`, ctx-cancellable). Pure helper the
  controller decides when to call it.

The level-triggered event loop lives on the inference controller, not
on any of these blocks. The loop is what was previously called the
"worker", the type has been deleted and its state moved up.

**Backend dispatch (`type`)**

| Type       | Path                                                     | Container       |
| ---------- | -------------------------------------------------------- | --------------- |
| `zeroshot` | Go → Python `/classify` (HuggingFace zero-shot pipeline) | Required        |
| `llm`      | Go → Python `/classify` (instruction-tuned LLM dispatch) | Required        |
| `api`      | Go → `<endpoint>/chat/completions` (OpenAI-compat)       | Not in the path |

For `api`, the Python container is irrelevant and the readiness gate
skips the `/health` probe. Failed remote calls bubble as per-article
errors, the article stays `processed=false` and the next Kick
retries.

**Python service** ([inference/main.py](../../inference/main.py))

When `type` is `zeroshot` or `llm`, a small FastAPI app runs in its
own container. It exposes `/health`, `/info`, `/load`, `/classify`,
and `/classify/batch`. It is stateless w.r.t. classifications every
request carries the full spec inline. `INFERENCE_MODEL` /
`INFERENCE_TYPE` env vars seed the model loaded at boot, the Go side
can swap the model at runtime via `POST /load`, which schedules a
background load and flips `/health` to `init` then back to `live`
once the new model finishes loading.

**Classification Flow**

The classify path is a level-triggered loop. A source insert kicks
the controller, the loop drains its in-memory queue article by
article, calling `Client.Classify(article, spec)` per article. Once
the queue is empty the loop calls `Mode.Persist(results)` (which
writes to the per-classification tables and, in continuous mode,
marks the source articles processed) then `Mode.Refill(cap)` for the
next batch. If refill returns articles the loop continues, if it
returns empty the `isRunning` flag flips false and the loop exits,
leaving no goroutine spinning. The next insert kicks it back on.

In `ManualMode` the classify-inline path (`Controller.ClassifyInline`)
bypasses the loop entirely: caller-supplied articles are classified
inline, persisted via the same `Mode.Persist`, and returned to the
caller. Kick from sources is a no-op in this mode.

The articles table is the durable buffer for continuous mode. Bursts
from webhooks or high-volume sources land there first and the loop
drains them at whatever rate the configured backend can sustain. On a
fast model (or a cheap remote endpoint) the loop reads a batch from
the DB, processes it, and asks for the next batch in single-digit
milliseconds there is no fixed-cadence cron tick capping throughput.
On a slow model the loop just stays busy longer between refill calls.

## Router

The HTTP layer at [pkg/server](../../server/pkg/server/server.go) uses the
standard library's `http.ServeMux`. The server type accepts seven
per-controller interfaces one per controller it routes to which means
handler tests can swap in lightweight fakes without touching any
controller code or database.

Each resource has its own routes file: system, sources, views, scheduler,
and classifications. Each file registers its handlers when the server
builds the mux. If the source controller exposes a webhook handler, the
server mounts it at `/webhooks/` as well.

A logger middleware wraps the mux and emits one INFO line per request
with the method, path, protocol, and remote address. Three response
helpers keep the handler bodies thin: one sets the content type and
encodes a payload at a given status, one wraps an error into the
structured error envelope, and a generic decoder reads a request body and
short-circuits with a `400` on malformed input.

Every log line that goes through `slog` also fans out through a
process-global broadcaster. The `/api/v1/system/logs` endpoint subscribes
to that broadcaster and streams each message as a Server-Sent Event so
the dashboard console can show a live tail without polling.

### API

Each route is exposed under both a `/api/v1/...` form and a shorter
legacy alias kept for the existing frontend. New code should target the
v1 form, the legacy aliases are scheduled for removal once the frontend
migrates fully.

> [!NOTE]
> Complete endpoint reference, with request and response shapes, is in
> [api.md](../reference/api.md).

## Persistence

The persistence layer under [pkg/registry](../../server/pkg/registry/)
groups SQL by resource. Each store wraps the shared `pgx` connection pool
and exposes a typed surface for one table family: articles,
classifications, monitor uptime, sources, views.

The base schema (sources, source_urls, articles, views,
monitor_uptime, plus the `upsert_monitor_uptime` PL/pgSQL function)
is embedded into the binary via `//go:embed base.sql` and applied
once at boot through `DB.EnsureBaseSchema`. Every CREATE uses
`IF NOT EXISTS`, and ENUM types are wrapped in DO blocks that
swallow `duplicate_object`, so the call is idempotent and safe to
re-run against an already-initialised database. Managed Postgres
deployments (RDS, Supabase, Neon, …) work without any external
migration tooling, the compose stack no longer needs the
`docker-entrypoint-initdb.d` mount it once did.

Stores use strict row scanning via `pgx.RowToAddrOfStructByName` so column
drift between Go structs and table columns surfaces as a clear error
instead of silent data loss. Bulk write paths article ingest, source
upsert, classification insert use `CopyFrom` into a temporary table
followed by an `INSERT ... SELECT ... ON CONFLICT` merge. That gives one
round-trip per batch regardless of size and keeps duplicate handling
explicit at the SQL layer.

Shared SQL helpers live in [pkg/registry/db](../../server/pkg/registry/db/where.go),
notably a chainable `WHERE` builder that accumulates predicates plus
named arguments and returns an empty clause when no predicates were added.
The builder lets read queries opt predicates in conditionally without
splintering the SQL across half a dozen branches.

Classification tables are dynamic: one table per classification declared
in the config, plus a metrics companion for fast aggregation queries.
The system controller's table sync creates and drops these tables to
match the config, the schema guard protects the dance from racing the
data plane.

Integration tests are gated by a `//go:build integration` tag and require
`TEST_DATABASE_URL` to be set. The shared harness in
[pkg/registry/dbtest](../../server/pkg/registry/dbtest/dbtest.go) opens
the pool once per test binary and exposes helpers for clearing tables
between tests.

## Testing

The test suite is layered.

Unit tests use stdlib `testing` plus `google/go-cmp` for structural
comparison. Each controller defines its dependencies as interfaces, so
controller tests substitute small hand-rolled fakes rather than real
stores or sibling controllers. Handler tests build a real `Server` with
seven fake `*Deps` interfaces and drive it through `httptest` so the
routing table and the JSON-encoding paths are both exercised.

Integration tests target the real Postgres schema and are gated by the
`integration` build tag. They live next to their store under
`*_integration_test.go` files and use the harness in `pkg/registry/dbtest`
to open a shared pool and clean tables between runs. The cross-process
contention between the live server and the test binary surfaces as
deadlock errors on schema mutations, the harness retries on `40P01` to
absorb it.

Race detection is on by default. The schema guard's contract writer
excludes readers, readers run concurrently, writer waits for readers is
covered by direct tests in `pkg/schema`, and indirectly by controller
tests that verify each RLock-wrapped path actually parks when a writer
holds the guard.

## Current Status and Known Gaps

The server is pre-production and tracks the broader FutureCast direction
described in [overview.md](overview.md).

Stable:

- Config loading, validation, `${VAR}` env expansion, `FTC_` overrides,
  hot reload via `Write` + `Reload`.
- Controller wiring and per-controller responsibilities.
- Source ingest for RSS, HTTP (with `extract` paths), and webhook
  (with HMAC enforcement when `auth.kind: hmac`).
- Per-source filter evaluation before insert.
- Continuous-mode classification loop against the self-hosted Python
  service (`type: zeroshot` / `llm`).
- Direct-to-API classification (`type: api`) against any
  OpenAI-compatible chat-completions endpoint, no Python container
  required.
- Manual-mode classify-inline via `Controller.ClassifyInline`
  (HTTP route handler still pending).
- Schema guard preventing the FK deadlock during config reloads.
- Embedded base-schema bootstrap via `EnsureBaseSchema`, managed
  Postgres deployments need no migration step.
- Dashboard API surface and SSE log streaming.
- Unit + integration test scaffolding, end-to-end test suites under
  `test/zeroshot/` and `test/api/`.

Active design areas:

- Manual-mode HTTP route (`POST /api/v1/classify`) wiring to
  `Controller.ClassifyInline`.
- HTTP-source pagination (parsed from config, not yet executed).
- Websocket source driver (config schema only, no driver registered).
- File-system source driver for offline corpora.
- Watch-based hot reload via `fsnotify` (manager-side support exists,
  currently disabled in favour of explicit `Write`/PUT).
- Validation pass for cron syntax, webhook path collisions, and
  label uniqueness at config load time.
