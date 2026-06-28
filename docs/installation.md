# Installation

FutureCast runs as two services, a Go server and a small Python inference container. The inference container is optional when running in `api` mode, which dispatches directly to an OpenAI-compatible endpoint (OpenRouter, Together, vLLM, etc.).

This guide covers Docker and building from source. Configuration is documented separately in [reference/config.md](./config.md).

**Prerequisites:** PostgreSQL 15+ instance reachable from the server. Docker Compose can spin one up for you (see below) or you can point at a managed one.

### Docker Compose(recommended)

1. Clone repository containing [compose.yml](..compose.yml) that holds the server blueprint. Templates for both inference server and database exist in the file.

```bash
git clone https://github.com/notallthere404/futurecast.git
cd futurecast
```

<br>

2. Modify example environment variables and config to match your specific source and classification schema. See [config.md](./config.md) for more information.

```bash
cp .env.example .env
cp config.example.yml config.yml
```

3. Initialize container(s)

```bash
docker compose up -d
```

4. _(Optional)_ View logs to ensure build is fully functioning (Pre-release version is unstable).

```bash
docker compose logs -f server
```

### Source

Requires Go 1.25+, Python 3.11+, and a reachable PostgreSQL instance.

Soon to be added...

## Next steps

- [Configuration reference](./config.md) — every YAML field, env var
  expansion, filter DSL.
- [Architecture](./architecture.md) — how the pieces fit together.
- [API reference](./api.md) — every HTTP route.
