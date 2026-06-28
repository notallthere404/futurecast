# FutureCast

[![Build](https://img.shields.io/github/actions/workflow/status/notallthere404/futurecast/release.yml?branch=main&label=build)](https://github.com/notallthere404/futurecast/actions/workflows/release.yml) [![Go Version](https://img.shields.io/github/go-mod/go-version/notallthere404/futurecast?filename=server/go.mod)](https://go.dev/) [![License](https://img.shields.io/github/license/notallthere404/futurecast)](./LICENSE) [![Status](https://img.shields.io/badge/status-pre--release-orange)](https://github.com/notallthere404/futurecast/releases) [![Go Report Card](https://goreportcard.com/badge/github.com/notallthere404/futurecast/server)](https://goreportcard.com/report/github.com/notallthere404/futurecast/server)

FutureCast is an open source framework built upon Large Language Models(LLMs) that ingests articles from sources classifies them with a model of your choice.

FutureCast relies on three core components:

- **Dynamic Classification:** Change your classification schema on the fly with FutureCast automatically updating the inference model and reconciling database tables.

- **Multiple Source Compatibility:** Set of data connectors that share a common configuration surface with definitions for data shape, protocol, scheduling and filtering. Sources can be designated as both fetchers or receivers.

- **Multimodal Configuration:** Setup FutureCast as a part of a large data pipeline or a smaller standalone module that handles data fetching, classification and data processing.

For the how, see [architecture](docs/architecture.md).

## Getting Started

You can run FutureCast in Docker or from source. See how in our published [installation](docs/installation.md) guide.

## Roadmap

- **Standalone CLI** - Alternative to the API drives the same inference path as the server, for one-shot classification of a file or stdin. Useful for batch processing without spinning up the full stack.

- **Semantic deduplication** - drop articles that are near-duplicates of recent ingest (embedding similarity over a sliding window), replacing the URL + dedupe-key dedupe with something that catches rephrased reposts.

- **First-party visualization** — server-rendered charts behind the same `View` API the dashboard consumes, so a deployment without the SvelteKit client still has visual output.

Longer term lives in [docs/roadmap/](docs/roadmap.md).

---

## Contributions

Stub

<!-- TODO -->

---

## License

MIT. See [LICENSE](LICENSE).
