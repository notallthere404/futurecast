# API Reference

The server exposes a versioned HTTP API at `/api/v1/*`. JSON in, JSON
out. Errors share a common envelope, success bodies vary per endpoint.

Legacy aliases under `/api/*` (without `/v1/`) are kept for backwards
compatibility and will be removed in a future release. New clients
should target `/api/v1/*` only.

---

## Table of Contents

- [Conventions](#conventions)
- [System](#system)
- [Sources](#sources)
- [Articles](#articles)
- [Scheduler](#scheduler)
- [Classifications](#classifications)
- [Visualizations](#visualizations)
- [Views](#views)
- [Webhooks](#webhooks)

---

## Conventions

### Request Format

- All `POST` / `PUT` bodies are JSON unless otherwise stated.
- Date ranges use RFC 3339 strings: `"2026-04-01T00:00:00Z"`.
- Path parameters are written as `{slug}`.
- Query parameters are documented per endpoint.

### Response Format

- Success bodies are JSON. Empty success returns `200 OK` (mutations
  may return `201 Created` or `204 No Content`).
- Errors use a single envelope:

```json
{
  "error": {
    "code": "view_not_found",
    "message": "view not found"
  }
}
```

The `code` is stable and machine-readable, clients should branch on it.
The `message` is for humans and may change between versions.

### Status Codes

| Code  | Meaning                              |
| ----- | ------------------------------------ |
| `200` | Success.                             |
| `201` | Resource created.                    |
| `204` | Success, no response body.           |
| `400` | Invalid JSON or bad request shape.   |
| `404` | Resource not found.                  |
| `500` | Internal error (logged server-side). |

---

## System

Server-level introspection and control.

### `GET /api/v1/system/status`

Health check. Returns `200 OK` with no body when the server is up.

<br/>

### `GET /api/v1/system/config`

Returns the current client-facing config block (`Raw` source string
and `ClassMap` of classification → attributes).

<br/>

### `PUT /api/v1/system/config`

Writes a new config file and triggers a server restart. Body:

```json
{ "config": "server:\n  port: 8765\n..." }
```

Returns `200 OK` on success, `500` if the new config fails to parse or
validate. Restart errors surface in the response body.

<br/>

### `POST /api/v1/system/restart`

Forces a restart using the current config file. Returns `200 OK`.

<br/>

### `GET /api/v1/system/logs`

Streams the in-memory log broadcaster. One log line per event,
reconnect to resume.

<br/>

### `POST /api/v1/system/uptime`

Returns the uptime percentage for a time window. Body:

```json
{ "start": "2026-04-01T00:00:00Z", "end": "2026-04-30T23:59:59Z" }
```

Response: a `float` between `0.0` and `1.0`.

<br/>

### `POST /api/v1/system/uptime/rate`

Returns bucketed uptime percentages. Body:

```json
{ "format": "day" }
```

`format` is one of `day` (24 hourly buckets) or `month` (30 daily
buckets). Response is an array of floats sized to the bucket count.

---

## Sources

CRUD over the configured sources plus manual run triggers.

### `GET /api/v1/sources`

Lists all sources. Response: `Source[]`.

<br/>

### `POST /api/v1/sources`

Upserts a single source. Body: a `Source` object (see the
[Configuration Reference](config.md) for the field list). Returns
`201`.

<br/>

### `POST /api/v1/sources/batch`

Upserts multiple sources atomically using `CopyFrom` + temp-table
merge. Body: `Source[]`. Returns `201`.

<br/>

### `POST /api/v1/sources/sync`

Triggers an immediate RSS fetch across all active RSS sources. Returns
`200 OK`.

<br/>

### `POST /api/v1/sources/rss/run`

Runs every active RSS source once immediately. Per-source cron entries
(`rss:<id>`) keep ticking on their own schedule, an overlap with the
next cron firing is harmless because the article inserter dedupes on
id.

---

## Articles

Read-only access to ingested article metrics.

### `GET /api/v1/articles/recent`

Returns the 10 most recently ingested articles, newest first.

<br/>

### `POST /api/v1/articles/rate`

Returns bucketed counts of articles over time. Body:

```json
{ "format": "day" }
```

`format` is one of `day` or `month`. Response is an `int[]` sized to
the bucket count.

---

## Scheduler

Cron introspection and control. Cron entries are registered by the
system controller during startup, one per active retriever source,
labelled `<type>:<id>`. There is no persisted jobs table and no
runtime endpoint to upsert entries, cadence comes from each source's
`schedule` field in `config.yaml`.

The classification loop is not cron-driven anymore, it runs as a
signal-triggered worker inside the inference controller. Sources kick
the worker after every successful article insert, and the worker
drains the article store until it is empty before going idle. The
`POST /api/v1/classifications/run` endpoint is the manual trigger for
that worker, see [Classifications](#classifications).

### `GET /api/v1/scheduler/status`

Lists registered cron entries with their next run time.

```json
[
  { "label": "rss:0f4a...", "next": "9m43s" },
  { "label": "http:c2a1...", "next": "4m02s" }
]
```

<br/>

### `POST /api/v1/scheduler/run`

Starts the cron scheduler. Returns `200 OK`. Idempotent.

<br/>

### `POST /api/v1/scheduler/stop`

Stops the cron scheduler. Returns `200 OK`. In-flight job functions
finish, future ticks do not fire until `run` is called again.

---

## Classifications

Search and bulk operations over classified articles.

### `GET /api/v1/classifications`

Searches classifications with query-string filters. All parameters are
optional.

| Param            | Meaning                                                |
| ---------------- | ------------------------------------------------------ |
| `classification` | Classification name (`events`, `actors`, …)            |
| `title`          | Substring match on article title                       |
| `label`          | Repeatable. Filters to articles tagged with this label |
| `start`, `end`   | RFC 3339 date range                                    |
| `cutoff`         | Float `0.0..0.99`, drops scores below threshold        |
| `limit`          | Int `1..200`. Default `50`                             |

Response: `LinkedClassification[]` (classification rows joined to the
article's `title` and `link`).

<br/>

### `POST /api/v1/classifications/count`

Returns the total number of classification rows for a classification
within a date range. Body:

```json
{
  "classification": "events",
  "start": "2026-04-01T00:00:00Z",
  "end": "2026-04-30T23:59:59Z"
}
```

Response: `int`.

<br/>

### `GET /api/v1/classifications/metrics`

Returns aggregated metrics (frequency, mean, variance) per label. The
query string mirrors the search filters.

| Param            | Meaning                                  |
| ---------------- | ---------------------------------------- |
| `classification` | Required                                 |
| `label`          | Repeatable. Filters to these labels only |
| `start`, `end`   | RFC 3339 date range                      |

Response: `{ "<label>": Signal }` where `Signal` has `frequency`,
`mean`, `variance`, and `count` fields.

<br/>

### `POST /api/v1/classifications/run`

Drains the inference queue: classifies pending articles and writes
results back. Returns `200 OK` immediately, processing is asynchronous.

<br/>

### `POST /api/v1/classifications/upload`

Bulk-inserts classification results from an external source (e.g.
re-running inference offline). Body: `ClassificationInsertItem[]`:

```json
[
  {
    "classification": "events",
    "id": "uuid",
    "article_id": "uuid",
    "timestamp": "2026-04-15T12:00:00Z",
    "data": { "vector": [{ "label": "Phishing", "score": 0.87 }] }
  }
]
```

Returns `200 OK`.

---

## Visualizations

Chart-shaped response endpoints. All accept JSON bodies and respond
with arrays sized for direct consumption by the dashboard.

### `POST /api/v1/visualizations/heatmap`

Returns daily score-frequency points for a label. Body:

```json
{
  "classification": "events",
  "label": "Prompt Injection",
  "start": "2026-01-01T00:00:00Z",
  "end": "2026-04-30T23:59:59Z"
}
```

Response: `LabelWeight[]` where each entry is `{ date, value }`.

<br/>

### `POST /api/v1/visualizations/treemap`

Returns label counts grouped by attribute. Body:

```json
{
  "classification": "events",
  "attribute": "vector",
  "start": "...",
  "end": "...",
  "cutoff": 0.0
}
```

Response: `LabelCount[]` where each entry is `{ label, count }`.

<br/>

### `POST /api/v1/visualizations/quadrant`

Compares frequency and mean confidence across two periods. Body:

```json
{
  "classification": "events",
  "label": "Initial Access",
  "a": { "start": "2026-03-01T00:00:00Z", "end": "2026-03-31T23:59:59Z" },
  "b": { "start": "2026-04-01T00:00:00Z", "end": "2026-04-30T23:59:59Z" }
}
```

Response: a single-element array of `LabelFrequencyAverage` with
`frequency` set to the ratio (b ÷ a) and `mean_confidence` set to the
delta (b − a).

<br/>

### `POST /api/v1/visualizations/plot`

Returns 2D points (timestamp × score) for a single label. Body:

```json
{
  "classification": "events",
  "label": "Phishing",
  "start": "...",
  "end": "..."
}
```

Response: `PlotPoint[]` with `article_id`, `title`, `link`, `label`,
`x` (timestamp), `y` (score).

<br/>

### `POST /api/v1/visualizations/scatter`

Returns 3D points (timestamp × score × label-vector-index). Body:

```json
{
  "classification": "events",
  "attribute": "vector",
  "cutoff": 0.0,
  "labels": ["Phishing", "Initial Access"],
  "start": "...",
  "end": "..."
}
```

Response: `ScatterPoint[]` with `article_id`, `title`, `link`, `label`,
`x` (timestamp), `y` (score), `z` (label index).

---

## Views

Stored dashboard configurations. Each view holds a list of panels,
each panel carries position metadata plus a declarative query that the
server resolves at GET time into a `Viz` envelope.

### `GET /api/v1/views`

Lists stored views. Optional query param:

| Param     | Meaning                                |
| --------- | -------------------------------------- |
| `user_id` | Filters to views owned by this user id |

Response: `View[]`.

<br/>

### `GET /api/v1/views/{slug}`

Returns a rendered view. The server runs every panel's `Query` and
fills `Viz` envelopes inline. Failing panels return their error in the
panel's `error` field rather than failing the whole view.

Response: `RenderedView`.

| Status | When                     |
| ------ | ------------------------ |
| `200`  | View found and rendered  |
| `404`  | No view with this slug   |
| `500`  | Database or render error |

<br/>

### `POST /api/v1/views`

Upserts a view. Body: `View` (the authoring shape, panels carry
queries, not data). Returns `201`.

<br/>

### `DELETE /api/v1/views/{slug}`

Deletes a view by slug. Returns `204 No Content`.

---

## Webhooks

Inbound endpoints registered by `source.webhook` entries. The full
route is `/webhooks{path}` where `{path}` matches the `path` field of
a configured webhook source.

These routes are not part of the `/api/v1/` namespace. They are
mounted at `/webhooks/` on the same mux and use the source's
configured `method`, `content_type`, `max_body_bytes`, and `auth`.

| Status | When                                           |
| ------ | ---------------------------------------------- |
| `202`  | Payload accepted and queued for classification |
| `400`  | Body invalid JSON or extract path failed       |
| `404`  | No webhook source registered with this path    |
| `405`  | Wrong HTTP method                              |
| `413`  | Body exceeds `max_body_bytes`                  |
| `415`  | Wrong `Content-Type`                           |

See [Configuration Reference → Webhook Source](config.md#webhook-source)
for per-source field documentation.
