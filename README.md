# Radaro

**Self-hosted social listening in one binary.** Track what people say about a keyword, brand or product across Hacker News, Reddit, Bluesky, Mastodon, Stack Overflow, RSS, X and YouTube. Radaro scores sentiment, clusters themes and shows it all in a local dashboard. It has no account and no telemetry, and the SQLite database stays on your machine.

![Radaro dashboard](docs/dashboard.png)

- **One binary.** Go, pure-Go SQLite, dashboard embedded. No Python, Node or runtime to install.
- **Zero-config first run.** `radaro demo` works offline. Hacker News, Bluesky and Stack Overflow need no keys.
- **Incremental + backfill scanning.** Durable per-source cursors catch up new mentions and walk back through history without re-fetching.
- **Local analysis.** Sentiment (lexicon with negation, intensifiers and contrast) and theme clustering run without any model. An LLM (Anthropic, OpenAI-compatible or local Ollama) is optional.
- **Alerts.** Slack / generic webhook and SMTP email for new negative mentions, volume spikes and sentiment drops. Delivery is durable: failed alerts retry on the next scan.
- **Agent-friendly CLI.** Every read command supports `--json`.

## Quickstart

```bash
go install github.com/verrloren/radaro/cmd/radaro@latest

radaro demo                         # synthetic dataset → dashboard at http://127.0.0.1:8042
radaro track "arch linux"           # live scan (Hacker News + Bluesky by default)
radaro serve                        # open the dashboard
```

Or build from source with `make build`, or run it in Docker with `docker compose up -d`.

## Commands

| Command | What it does |
|---|---|
| `radaro demo` | Load a bundled synthetic dataset and open the dashboard |
| `radaro track <keyword>` | Fetch new mentions, analyze, store, alert (`--sources`, `--limit`, `--pages`, `--project`) |
| `radaro backfill <keyword>` | Fetch older pages via saved cursors (`--pages`) |
| `radaro watch <keyword>` | Scan on an interval (`--every 900`, `--runs N`) |
| `radaro report [keyword]` | Sentiment, sources, top themes and quotes |
| `radaro serve` | Local dashboard + JSON API (`--port 8042`, `--host 127.0.0.1`) |
| `radaro project list\|create\|add\|remove\|delete\|report` | Group keywords and report across them |
| `radaro export [keyword]` | Full records as JSON or CSV (`-f csv -o file.csv`) |
| `radaro sources` | Available sources and whether they are configured |
| `radaro test-alert` | Send a synthetic alert (`--transport webhook\|email`, `--kind negative\|volume\|sentiment`) |

Global flags: `--db <path>` (default `radaro.db` or `$RADARO_DB`) and `--json`.

## Sources

| Source | Needs |
|---|---|
| Hacker News | nothing (Algolia API) |
| Bluesky | nothing (public AppView; the anonymous API serves only the latest page, so no deep backfill) |
| Stack Overflow | nothing (anonymous daily quota applies) |
| Reddit | `RADARO_REDDIT_CLIENT_ID` + `RADARO_REDDIT_CLIENT_SECRET`, or `RADARO_REDDIT_ACCESS_TOKEN` |
| Mastodon | `RADARO_MASTODON_ACCESS_TOKEN` (+ `RADARO_MASTODON_INSTANCE`) |
| RSS / Atom | `RADARO_RSS_FEEDS` |
| X | `RADARO_X_BEARER_TOKEN` (API v2 recent search) |
| YouTube | `RADARO_YOUTUBE_API_KEY` |

A failing source never sinks a scan. Its error is recorded and the other sources still land. Every option is listed in [`.env.example`](.env.example).

## Alerts

Set `RADARO_WEBHOOK_URL` (a Slack incoming webhook or any HTTP endpoint), or set `RADARO_EMAIL_TO`, `RADARO_EMAIL_FROM` and `RADARO_SMTP_HOST`. On each scan:

- newly ingested **negative mentions** are batched per target;
- optional thresholds fire on a **volume spike** (`RADARO_ALERT_VOLUME_MULTIPLIER`) or a **net-sentiment drop** (`RADARO_ALERT_SENTIMENT_DROP`), compared with the preceding windows, with a cooldown.

Delivery state lives in SQLite, so nothing is sent twice and failures are retried. Check your setup with `radaro test-alert`.

## HTTP API

`radaro serve` exposes the JSON API the dashboard uses, documented in [docs/API.md](docs/API.md). The server binds to `127.0.0.1` and has no authentication. If you expose it, put it behind an authenticated reverse proxy.

## Development

```bash
make test        # go test ./...
make web         # rebuild the React dashboard into web/dist (committed, embedded via go:embed)
make build       # ./radaro
npm --prefix web run dev   # dashboard dev server with /api proxied to 127.0.0.1:8042
```

## Credits

Radaro is an independent Go rewrite inspired by [Harken](https://github.com/VladUZH/harken) (MIT). Its source adapters, cursor-based scanning, lexicon sentiment analyzer and theme clustering are ported from Harken. See [NOTICE](NOTICE).

## License

MIT, see [LICENSE](LICENSE).
