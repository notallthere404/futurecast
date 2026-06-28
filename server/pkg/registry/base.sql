-- Base schema. Idempotent: safe to re-run on every server startup
-- against a fresh or already-initialised database. ENUM types use a DO
-- block because Postgres has no CREATE TYPE IF NOT EXISTS.

DO $$ BEGIN
    CREATE TYPE source_type AS ENUM('rss', 'page', 'webhook', 'http', 'websocket');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE source_trust AS ENUM('unknown', 'low', 'medium', 'high');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE source_target AS ENUM('content', 'description', 'link');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

DO $$ BEGIN
    CREATE TYPE source_url_type AS ENUM('completed', 'retry', 'discover');
EXCEPTION WHEN duplicate_object THEN NULL; END $$;

CREATE TABLE IF NOT EXISTS sources (
    id uuid PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    type source_type NOT NULL,
    name varchar(255) NOT NULL,
    description text NOT NULL DEFAULT '',
    tags text[] NOT NULL DEFAULT '{}',
    dedupe_key text,
    timeout_seconds int,
    retry jsonb,
    headers jsonb,
    auth jsonb,
    extract jsonb,
    spec jsonb NOT NULL DEFAULT '{}'::jsonb,
    hash bytea NOT NULL,
    url text NOT NULL UNIQUE,
    created_at timestamptz DEFAULT now(),
    updated_at timestamptz DEFAULT NULL,
    active bool DEFAULT false,
    trust source_trust NOT NULL DEFAULT 'unknown'
);

CREATE INDEX IF NOT EXISTS sources_type_active_idx
    ON sources(type, active) WHERE active = true;

CREATE TABLE IF NOT EXISTS source_urls (
    id SERIAL PRIMARY KEY,
    source_id uuid NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    type source_url_type NOT NULL,
    url text NOT NULL UNIQUE,
    error varchar(255),
    retry_count int2
);

CREATE INDEX IF NOT EXISTS source_urls_source_completed_idx
    ON source_urls(source_id)
    WHERE type = 'completed' OR (type = 'retry' AND retry_count <= 0);

CREATE INDEX IF NOT EXISTS source_urls_source_discover_idx
    ON source_urls(source_id)
    WHERE type = 'discover';

CREATE TABLE IF NOT EXISTS articles (
    id uuid PRIMARY KEY NOT NULL DEFAULT gen_random_uuid(),
    source_id uuid NOT NULL,
    source_type source_type NOT NULL,
    title varchar(255) NOT NULL,
    content text NOT NULL,
    timestamp timestamptz DEFAULT now(),
    link text,
    processed bool DEFAULT false
);

CREATE INDEX IF NOT EXISTS articles_source_idx ON articles(source_id);
CREATE INDEX IF NOT EXISTS articles_processed_idx
    ON articles(processed) WHERE processed = false;

CREATE TABLE IF NOT EXISTS views (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    slug        text NOT NULL UNIQUE,
    title       text NOT NULL,
    description text NOT NULL DEFAULT '',
    user_id     uuid,
    panels      jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at  timestamptz DEFAULT now(),
    updated_at  timestamptz
);

CREATE INDEX IF NOT EXISTS views_slug_idx ON views (slug);
CREATE INDEX IF NOT EXISTS views_user_idx ON views (user_id);

CREATE TABLE IF NOT EXISTS monitor_uptime (
    id serial PRIMARY KEY,
    start timestamptz NOT NULL DEFAULT NOW(),
    recent timestamptz NOT NULL DEFAULT NOW(),
    up boolean NOT NULL DEFAULT 'true'
);

CREATE INDEX IF NOT EXISTS monitor_uptime_recent_idx
    ON monitor_uptime (recent DESC);

CREATE OR REPLACE FUNCTION upsert_monitor_uptime(threshold_seconds integer)
RETURNS bigint
LANGUAGE plpgsql
AS $$
DECLARE
    latest_row monitor_uptime%rowtype;
    new_id bigint;
    observed_at timestamptz;
BEGIN
    IF threshold_seconds <= 0 THEN
        RAISE EXCEPTION 'threshold_seconds must be positive';
    END IF;

    -- Serialize rollover inserts; row locks cannot cover empty-table or new-latest-row races.
    PERFORM pg_advisory_xact_lock(9384721);
    observed_at := clock_timestamp();

    SELECT *
    INTO latest_row
    FROM monitor_uptime
    ORDER BY recent DESC
    LIMIT 1;

    IF NOT FOUND OR observed_at - latest_row.recent > make_interval(secs => threshold_seconds) THEN
        INSERT INTO monitor_uptime (start, recent, up)
        VALUES (observed_at, observed_at, true)
        RETURNING id INTO new_id;

        RETURN new_id;
    END IF;

    UPDATE monitor_uptime
    SET recent = observed_at,
        up = true
    WHERE id = latest_row.id;

    RETURN latest_row.id;
END;
$$;
