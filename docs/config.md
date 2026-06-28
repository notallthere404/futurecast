# Configuration Reference

The server reads `config.yaml` at startup. Resolution order: explicit
`Set` > flag > env var > config file > default. Env vars use prefix
`FTC_` with dots replaced by underscores (`server.port` →
`FTC_SERVER_PORT`). String values inside the YAML can also reference
process env vars via `${VAR}` — expansion runs at load time before
validation.

Hot reload is supported for every value below: writing to the file (or
calling `PUT /api/v1/system/config`) re-runs validation, swaps the
snapshot atomically, and pushes the new state through every
subscriber. Failed validation keeps the previous config in place; a
bad save never crashes the server.

---

## Table of Contents

- [Top-Level Keys](#top-level-keys)
- [Server](#server)
- [Source](#source)
  - [Inherited Defaults](#inherited-defaults)
  - [RSS Source](#rss-source)
  - [HTTP Source](#http-source)
  - [Webhook Source](#webhook-source)
  - [WebSocket Source](#websocket-source)
- [Inference](#inference)
  - [Engine Settings](#engine-settings)
  - [Classifications](#classifications)
  - [Attribute Fields](#attribute-fields)
- [Filter DSL](#filter-dsl)
  - [Target](#target)
  - [Generator](#generator)
  - [Operator](#operator)
  - [Value](#value)
  - [Examples](#examples)
- [Environment Variables](#environment-variables)
- [Hot Reload](#hot-reload)
- [Validation](#validation)

---

## Top-Level Keys

```yaml
server: { ... }
source: { ... }
inference: { ... }
```

Each section can be omitted; defaults apply.

---

## Server

Application-level settings. Not source- or inference-specific.

**Host**

```yaml
host: "0.0.0.0"
```

Sets the bind address. Default `localhost`. Use `0.0.0.0` to accept
external connections.

<br/>

**Port**

```yaml
port: 8765
```

Sets the listen port. Default `8765`. Valid range `1..65535`. Invalid
values fail validation at load time.

<br/>

**Log Level**

```yaml
log_level: "info"
```

Sets the log verbosity. Default `info`. Options:

- `debug` — verbose tracing of internal state changes
- `info` — operational events (startup, reload, fetch summaries)
- `warn` — recoverable issues that may need attention
- `error` — failures that affect functionality

<br/>

**External Database**

```yaml
ext_db: "postgres://user:pass@host:5432/FTC"
```

Sets the external database DSN. When unset, the server reads
`DATABASE_URL` from the environment instead. The environment variable
always wins over this value.

---

## Source

The `source` block holds defaults inherited by every source entry plus
per-kind lists of source instances. Child values override defaults;
lists and maps **replace** the inherited value (no merge). Set a list to
`[]` to disable an inherited filter.

### Inherited Defaults

These keys apply to every source under `source.rss`, `source.http`,
`source.webhook`, `source.websocket` unless the source overrides them.

**Schedule**

```yaml
schedule: "*/10 * * * *"
```

Sets the cron expression for fetch frequency. Default `*/10 * * * *`.
Standard 5-field crontab; see [crontab.guru](https://crontab.guru).
Ignored by listener kinds (`webhook`, `websocket`).

<br/>

**Infer**

```yaml
infer: ["events"]
```

Lists the classifications to route every fetched article through. Empty
list disables classification entirely. Names must match top-level keys
under `inference:`. Per-source override is possible.

<br/>

**Trust**

```yaml
trust: "medium"
```

Sets the trust level for the source. Default `medium`. Surfaces in
dashboards and is available to filter conditions. Options:

- `unknown` — provenance unverified
- `low` — known source with frequent inaccuracies
- `medium` — reliable for most signals
- `high` — authoritative; weighted heavily by analysis

<br/>

**Timeout (Seconds)**

```yaml
timeout_seconds: 30
```

Sets the per-fetch deadline. Default `30`. A timeout cancels the
in-flight request; the next cron tick runs as scheduled.

<br/>

**Filter**

```yaml
filter:
  - "content.len.gte.200"
  - "!title.contains.eq.spam"
```

Defines pre-persist drop rules. Each entry is a DSL string evaluated
against the article. Failed parse at load time prevents server start;
runtime evaluation logs and skips on error. See [Filter DSL](#filter-dsl)
below.

<br/>

**Retry**

```yaml
retry:
  max: 3
  backoff_ms: 500
  max_delay_ms: 30000
```

Defines the transient failure policy for outbound requests (rss, http,
websocket). `max` is the total attempt count. `backoff_ms` doubles each
attempt up to `max_delay_ms`.

<br/>

**Headers**

```yaml
headers:
  Accept: "application/rss+xml"
  X-Tenant: "ftc"
```

Lists static request headers added to every outbound fetch (rss, http,
websocket handshake). Webhook ignores; inbound headers are validated
elsewhere.

### RSS Source

Array of RSS source definitions. Each entry inherits the defaults above.

**Name**

```yaml
name: "CISA"
```

Required. Sets the display name. Should be unique within the config.

<br/>

**URL**

```yaml
url: "https://www.cisa.gov/cybersecurity-advisories/all.xml"
```

Required. Sets the feed URL.

<br/>

**Active**

```yaml
active: true
```

Toggles whether the source is fetched. Default `true`. Inactive sources
remain in config but skip scheduling.

<br/>

**Target**

```yaml
target: "description"
```

Selects the feed field used as article content. Default `description`.
Options:

- `content` — uses `<content:encoded>` (full article)
- `description` — uses `<description>` (summary or excerpt)
- `link` — fetches the linked page and applies `selectors`

<br/>

**Limit**

```yaml
limit: 50
```

Caps items per fetch. Default `0` (no cap). Useful for high-volume feeds.

<br/>

**Paths**

```yaml
paths: ["*"]
```

Lists URL glob patterns the page scraper follows. Used only when
`target: link`. `["*"]` matches every link.

<br/>

**Selectors**

```yaml
selectors:
  nav: ["#blog-pager > a"]
  title: ["h1.entry-title"]
  content: ["#article-body"]
```

Maps CSS selector arrays to page-scrape roles. Used only when
`target: link`. The first selector that matches wins per role.

### HTTP Source

Scheduled HTTP requester. Inherits defaults; adds:

**Method**

```yaml
method: "GET"
```

Sets the HTTP method. Default `GET`. Options: `GET`, `POST`, `PUT`,
`PATCH`, `DELETE`.

<br/>

**Query**

```yaml
query:
  limit: "100"
  format: "json"
```

Lists URL query parameters. Values are stringified.

<br/>

**Body**

```yaml
body: '{"query":"type:incident"}'
```

Sets the request body for POST / PUT / PATCH. Plain string; templating
is not applied.

<br/>

**Auth**

```yaml
auth:
  kind: "api_key"
  header: "x-apikey"
  token: "${VT_API_KEY}"
```

Defines outbound auth. Options for `kind`:

- `bearer` — sets `Authorization: Bearer <token>`
- `api_key` — sets `<header>: <token>`
- `basic` — sets `Authorization: Basic <base64(user:pass)>`
- `hmac` — signs the request body with `secret` (used mostly inbound)
- `header` — sets `<header>: <token>` (no scheme)

`${VAR}` expansion reads from the process environment at load time. A
missing variable fails the load.

<br/>

**Extract**

```yaml
extract:
  items: "data"
  title: "attributes.meaningful_name"
  content: "attributes.last_analysis_stats"
  timestamp: "attributes.last_analysis_date"
  link: "attributes.url"
```

Maps JSON paths from the response payload into `Article` fields. `items`
points at the array of records; the other paths are relative to one
record. `timestamp` accepts RFC3339 strings or Unix seconds (float or
int). `content` is required; missing causes the record to be dropped.

<br/>

**Pagination**

```yaml
pagination:
  cursor_path: "meta.cursor"
  next_url_path: ""
  page_param: "cursor"
  max_pages: 10
```

Defines pagination behavior. Use `cursor_path` + `page_param` for
cursor-based APIs (the cursor value is sent back as the named query
param on the next call). Use `next_url_path` when the response carries
a full next-page URL. `max_pages` caps the walk per fetch tick.

### Webhook Source

Inbound endpoint. The server mounts `path` on the public mux at startup.

**Path**

```yaml
path: "/alerts"
```

Required. Sets the URL path beneath the webhook prefix; the full route
is `/webhooks/<path>`. Both `alerts` and `/alerts` work — the
registration normalises the leading slash so config style is not
load-bearing. Must be unique across webhook sources.

<br/>

**Method**

```yaml
method: "POST"
```

Sets the HTTP method to accept. Default `POST`. Other methods return
`405`. Options: `POST`, `PUT`, `PATCH`.

<br/>

**Content Type**

```yaml
content_type: "application/json"
```

Sets the required request `Content-Type`. Default `application/json`.
Mismatches return `415`.

<br/>

**Max Body Bytes**

```yaml
max_body_bytes: 1048576
```

Caps the request body size. Default `1048576` (1 MiB). Exceeding returns
`413`.

<br/>

**Replay Window Seconds**

```yaml
replay_window_seconds: 300
```

Sets the window for HMAC nonce / timestamp replay rejection. `0`
disables replay protection.

<br/>

**Auth**

```yaml
auth:
  kind: "hmac"
  header: "X-Signature"
  secret: "${WEBHOOK_SECRET}"
```

Defines inbound auth. `hmac` validates the `<header>` value against
`hex(HMAC-SHA256(secret, body))` with a constant-time compare; missing
header returns `401`, mismatch returns `403`. `bearer`, `api_key`,
and `header` are parsed but currently fall through as no-op for
inbound webhooks — only `hmac` is enforced today. `basic` is not
supported inbound.

<br/>

**Extract**

Same shape as `source.http.extract`. Required to populate `Article`
fields from the inbound payload.

### WebSocket Source

Long-running connection. Inherits defaults; adds:

**URL**

```yaml
url: "wss://example.com/stream"
```

Required. Sets the WSS or WS endpoint.

<br/>

**Protocols**

```yaml
protocols: ["v2", "v1"]
```

Lists subprotocols sent in the `Sec-WebSocket-Protocol` header. The
server picks the first it supports.

<br/>

**Subscribe**

```yaml
subscribe: '{"action":"subscribe","topic":"alerts"}'
```

Sets a message sent immediately after the handshake. Sent as text or
binary per `message_type`.

<br/>

**Message Type**

```yaml
message_type: "json"
```

Sets the frame parsing strategy applied before `extract` runs. Default
`json`. Options:

- `json` — parses each frame as JSON
- `text` — treats the frame as a UTF-8 string
- `binary` — passes bytes through without parsing

<br/>

**Buffer Size**

```yaml
buffer_size: 1024
```

Sets the read buffer size in bytes. Default `1024`.

<br/>

**Heartbeat**

```yaml
heartbeat:
  interval_ms: 30000
  timeout_ms: 10000
```

Defines the ping interval and pong wait. `interval_ms: 0` disables
heartbeats.

<br/>

**Reconnect**

```yaml
reconnect:
  enabled: true
  backoff_ms: 1000
  max_delay_ms: 60000
```

Defines the auto-reconnect policy. `backoff_ms` doubles per attempt up
to `max_delay_ms`.

---

## Inference

Classifier configuration. The block holds engine settings plus dynamic
top-level classification keys.

### Engine Settings

**Mode**

```yaml
mode: "continuous"
```

Sets when inference runs. Default `continuous`. Options:

- `continuous` — level-triggered background loop. Source inserts kick
  the loop; it drains the unprocessed-articles table, persists
  results, and exits when there is nothing left to refill.
- `manual` — no background loop. Classifications fire only when an
  external caller hands articles to the controller via the
  `/classify` route (or a future CLI). Source-insert kicks are no-ops
  in this mode.

<br/>

**Type**

```yaml
type: "api"
```

Defines the inference backend. Default `zeroshot`. Options:

- `zeroshot` — self-hosted Hugging Face zero-shot pipeline. Server
  talks to the Python container at `inference.addr`.
- `llm` — self-hosted instruction-tuned LLM. Same transport as
  zeroshot; the Python side dispatches on its loaded classifier.
- `api` — remote OpenAI-compatible chat-completions endpoint. The Go
  server dispatches directly; no Python container in the path.
  Requires the [`api` block](#api). Validation fails the load when
  `endpoint`, `api_key`, or top-level `model` is missing.

<br/>

**Engine**

```yaml
engine: "transformers"
```

Picks the self-hosted runtime when `type` is `zeroshot` or `llm`.
Currently only `transformers` is wired. Ignored for `type: api`.

<br/>

**Addr**

```yaml
addr: "http://inference:8080"
```

Base URL of the Python inference service. Defaults to
`http://inference:8080` (the compose-network hostname). Ignored for
`type: api`.

<br/>

**Model**

```yaml
model: "Qwen/Qwen3-Reranker-8B"
```

Sets the model identifier. For `zeroshot` and `llm`, this is the
Hugging Face hub id (or a local path mounted into the inference
container). For `api`, this is the remote model id understood by the
configured endpoint (e.g. `meta-llama/llama-3.1-8b-instruct` on
OpenRouter).

<br/>

**Cutoff**

```yaml
cutoff: 0.0
```

Sets the global score cutoff. Predictions below this score are dropped.
Attribute entries can override. Range `0.0..1.0`.

<br/>

**Top N**

```yaml
top_n: 3
```

Caps the maximum labels returned per article. Attribute entries can
override.

<br/>

**API**

```yaml
api:
  endpoint: "https://openrouter.ai/api/v1"
  api_key: "${OPENROUTER_API_KEY}"
  model_override: ""
```

Defines remote API settings. Consulted only when `type: api`.
`endpoint` should be the chat-completions base (the server appends
`/chat/completions`); any OpenAI-compatible provider works (OpenRouter,
OpenAI, Together, Groq, vLLM, Anthropic's compat endpoint, …).
`api_key` is sent as a `Bearer` token. `model_override` is currently
unused — the top-level `model` is always sent — but the field is
reserved so a future per-endpoint override does not require a schema
change.

Validation requires `endpoint`, `api_key`, and top-level `model` to be
non-empty when `type: api`. Missing values fail the load with a clear
error rather than surfacing later as per-article 401s.

### Classifications

Each classification is a top-level key under `inference:` whose value is
an array of attributes. The classification name is referenced by
`source.infer:`.

```yaml
inference:
  events:
    - name: vector
      ...
  actors:
    - name: profile
      ...
```

### Attribute Fields

**Name**

```yaml
name: "vector"
```

Required. Sets the attribute identifier. Must be unique within its
classification.

<br/>

**Prompt**

```yaml
prompt: "{label}: {definition}"
```

Defines the template used per label. `{label}` and `{definition}` are
substituted at classification time.

<br/>

**Instruction**

```yaml
instruction: "You are a cyber threat intelligence analyst. Determine if the document describes the following technical procedure, tactic, or vulnerability."
```

Sets the system instruction passed to the classifier alongside the
prompt.

<br/>

**Labels**

```yaml
labels:
  - name: "Reconnaissance"
    definition: "Adversary gathering information to plan future operations."
  - name: "Initial Access"
    definition: "Adversary trying to get into the network."
```

Defines the label set. The paired-table form keeps names and
definitions together; the flat-string form (`labels: ["A", "B"]`) is
also accepted via a mapstructure DecodeHook that promotes each string
to `{name: A, definition: ""}` at parse time. Use the flat form for
quick zero-shot setups where the label name is self-explanatory; use
the paired form when running `type: llm` or `type: api` so the
classifier has explicit semantics for each label.

<br/>

**Cutoff**

```yaml
cutoff: 0.1
```

Overrides the global `inference.cutoff` for this attribute. Omitting
inherits the global value.

<br/>

**Top N**

```yaml
top_n: 5
```

Overrides the global `inference.top_n` for this attribute.

---

## Filter DSL

Filter strings take the form:

```
[!]<target>.[<generator>.]<operator>.<value>
```

A leading `!` negates the result. Strings split on the first two or
three dots; the value may contain dots (e.g. `score.gte.0.85`).

### Target

Names the field to evaluate. Common values: `content`, `title`, `link`,
`timestamp`, `tags`, `score`. Empty or `*` is a wildcard that matches on
the first field that satisfies the condition.

### Generator

Optional. Derives a comparable value from the target. Omitting it falls
back to identity (string compare). Options:

- `len` — string length, returns int
- `regex` — pattern match, returns bool
- `contains` — substring (string) or membership (array), returns bool
- `empty` — field is empty, returns bool
- `sim` — semantic similarity (requires inference), returns float
- `count` — array length, returns int

### Operator

Comparison applied to the generator's output. Options: `eq`, `ne`, `gt`,
`gte`, `lt`, `lte`. Boolean generators (`regex`, `contains`, `empty`)
pair with `eq.true` or `eq.false`; the `!` prefix is sugar for toggling
that value.

### Value

Literal compared against the generator's output. The literal type must
match the generator's output type:

- int for `len` and `count`
- float for `sim`
- bool for `regex`, `contains`, and `empty`
- string for the default identity generator

### Short form

The 3-part `<gen>.<op>.<value>` form drops the explicit target and
defaults to wildcard. Convenient for length / regex / contains rules
that should apply across fields:

```yaml
filter:
  - "len.gte.100"          # equivalent to "*.len.gte.100"
  - "contains.eq.cyber"    # equivalent to "*.contains.eq.cyber"
```

The parser disambiguates by checking whether the first token is a
known generator (`len`, `regex`, `contains`, `count`, `empty`, `sim`).
If it is, the wildcard fallback applies; otherwise the token is
treated as the target.

### Examples

```yaml
filter:
  - "content.len.gte.200"        # content at least 200 chars
  - "content.len.lte.5000"       # content at most 5000 chars
  - "title.regex.eq.^CVE-\\d{4}" # title starts with CVE-YYYY
  - "tags.contains.eq.alert"     # tags contain "alert"
  - "!tags.contains.eq.spam"     # tags do not contain "spam"
  - "score.gte.0.85"             # score field >= 0.85
  - ".len.gte.50"                # any field length >= 50 (explicit wildcard)
  - "len.gte.50"                 # same — short form
```

---

## Environment Variables

Two mechanisms read from the process environment. They cover different
shapes and don't conflict.

### `${VAR}` expansion inside YAML

Strings of the form `${VAR_NAME}` (or `$VAR_NAME`) inside any YAML
string value are expanded at load time. The expansion runs through a
custom mapstructure DecodeHook in `pkg/config` that calls
[`os.ExpandEnv`](https://pkg.go.dev/os#ExpandEnv) on every string
during `Unmarshal`. Missing variables expand to the empty string;
downstream validators (e.g. `inference.api.api_key`) reject the
empties with a clear error so a missing key fails the load rather
than surfacing later as a per-request 401.

Use this for secrets and arbitrary nested values that don't have a
viper default:

```yaml
inference:
  api:
    api_key: "${OPENROUTER_API_KEY}"

source:
  webhook:
    - auth:
        secret: "${WEBHOOK_SECRET}"
```

### `FTC_` flat overrides

Viper's `AutomaticEnv` is wired with prefix `FTC_` and a
dot-to-underscore key replacer. Any key that has a default registered
in `setDefaults` (in `pkg/config/config.go`) can be overridden from
the environment without touching the YAML:

```bash
FTC_SERVER_PORT=9000           ./server
FTC_SERVER_LOG_LEVEL=debug     ./server
FTC_INFERENCE_DEFAULT_TOP_N=5  ./server
```

The default set today: `server.host`, `server.port`,
`server.log_level`, `source.default.schedule`, `source.default.trust`,
`inference.addr`, `inference.engine`, `inference.default.cutoff`,
`inference.default.top_n`. Keys outside this set (per-source
instances, dynamic classification names, `inference.api.*`) are not
visible to `AutomaticEnv` — use `${VAR}` expansion instead.

The env layer takes precedence over the YAML value but loses to an
explicit CLI flag of the same name (when flags exist).

---

## Hot Reload

Reloads are triggered explicitly today via either path:

- `PUT /api/v1/system/config` with a new YAML body — the system
  controller writes the file to disk and re-runs the boot sequence
  against the new snapshot.
- Calling `ConfigManager.Reload()` from inside the process.

On reload:

1. The new file is parsed and `${VAR}` expansion runs against the
   current process environment.
2. `Validate()` runs. A failure logs the error and keeps the previous
   config; the server stays on the last good snapshot.
3. On success, the new config is published atomically and registered
   subscribers (source controller, scheduler controller, inference
   controller, …) react. The inference controller compares the
   reload-relevant fields (`type`, `model`, `engine`) and only swaps
   the dispatch target / re-runs `/load` when one of them changed.

File-watch hot reload via `fsnotify` is wired in the manager but
disabled by default; turn it on in `ConfigManager.Init` when you're
comfortable with editor-save semantics (vim / neovim / some JetBrains
configs replace the inode on save and would miss the watch event
without extra handling).

---

## Validation

The following are checked at load time and on every reload. Failures
keep the previous good snapshot in place rather than crashing:

- `server.port` falls in range `1..65535`.
- Every attribute prompt matches its inference type — `zeroshot`
  prompts must contain `{}` (the HuggingFace `hypothesis_template`
  placeholder); `llm` prompts must contain `{label}`. `api` prompts
  are unvalidated because remote endpoints own their template
  semantics.
- When `type: api`, `inference.api.endpoint`, `inference.api.api_key`,
  and top-level `inference.model` are all non-empty.
- Every `filter` string parses cleanly (target, generator, operator
  known). Failure here aborts the load before any source registers.

Not yet validated at load time (caught at use):

- `source.infer` entries referencing a non-existent classification.
- `auth.kind` recognition (unknown kinds fall through as no-op on the
  inbound webhook path).
- Cron expression syntax (the scheduler logs per-source on `Add`
  failure but doesn't abort the load).
- Webhook path collisions across sources.
- Label uniqueness within a classification.

These are tracked for a future validation pass.
