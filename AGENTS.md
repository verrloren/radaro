# AGENTS.md — working on the Radaro codebase

These notes are for coding agents (Codex, Claude Code, Cursor, …) that change this repository.

**If you only want to *use* Radaro to promote a project, you need the skill, not this file:** [`skills/radaro/SKILL.md`](skills/radaro/SKILL.md). Install it with `radaro skill install`.

## What Radaro is

Radaro is a single Go binary for self-hosted social listening and human-approved publishing, for one person or a small team on one server:

1. **Scan.** `track` / `watch` / `backfill` pull mentions from HN, Bluesky, Stack Overflow, Reddit, Mastodon, RSS, X and YouTube into SQLite.
2. **Analyze.** Lexicon sentiment and term-frequency themes, with an optional LLM.
3. **Alert.** Webhook / Slack / SMTP alerts for negative mentions, volume spikes and sentiment drops.
4. **Publish.** `opportunities` → `draft` → `approve` → `publish` to Reddit, Bluesky, Mastodon and Dev.to via their public APIs → `stats`.
5. **Dashboard.** `serve` runs the React dashboard, embedded in the binary, plus the JSON API.
6. **Users.** Email + password accounts with JWT sessions. Each user has projects; a project has keywords and one account per platform.

Design constraints, don't break them:

- **One self-contained binary.** No runtime, no Docker, and no external service is required. The SQLite driver is pure Go (`modernc.org/sqlite`), so builds use `CGO_ENABLED=0`. The dashboard is embedded with `go:embed`.
- **The binary has no built-in LLM agent.** The user's own coding agent does the thinking; Radaro provides data, state and platform access. Keep the CLI `--json`-friendly for agents.
- **Humans approve every publish.** Only drafts in status `approved` can be published, and publish claims the draft (`publishing`) before any network call. Don't add shortcuts around this.
- **Everything is per user.** Every `/api/*` route except sign-in sits behind `requireAuth` (a test walks all routes). Store methods take the user id and filter in SQL; another user's resource answers `404`, like a missing one. User id 0 means the server itself (admin commands, demo) and is never used by handlers. Mentions and scan state are shared per keyword on purpose.
- **The CLI is an API client.** Data commands call the server as the signed-in user (`internal/client`). Only `serve`, `demo`, `test-alert` and `admin *` open the database.
- **Sessions.** Browser: httpOnly `SameSite=Lax` cookies, never tokens in page scripts or localStorage. CLI: `X-Radaro-Client: cli` and `Authorization: Bearer`. Access JWTs are HS256 only; refresh tokens are stored hashed and rotate with reuse detection.
- **MIT license.** Third-party license notices are retained in `NOTICE`. Postiz is AGPL: never copy its code; platform connectors are written from the platforms' public API docs.
- **Private by default.** No telemetry. Credentials stay in the database (chmod 600) and never leave the server. The HTTP server binds to 127.0.0.1 by default, expects a TLS proxy in front when exposed, and rejects cross-origin writes.

## Layout

```
cmd/radaro/          Cobra CLI (one file per command group)
internal/model       Mention type and stable content-hash IDs
internal/sources     scan adapters; registry in source.go
internal/pipeline    fetch (concurrent) → sentiment → store → themes → alerts
internal/analyze     lexicon sentiment, TF theme clustering
internal/llm         optional providers (anthropic, openai-compatible, ollama)
internal/store       SQLite schema, step migrations (migrate.go) + queries (users, sessions, projects, keywords, mentions, scan state, alerts, accounts, drafts, activity)
internal/auth        passwords (bcrypt), JWT access tokens, refresh-token rotation
internal/client      the CLI's HTTP client and saved session
internal/alerts      webhook/Slack + SMTP delivery, threshold evaluation
internal/publish     platform connectors: reddit, bluesky, mastodon, devto
internal/server      chi JSON API (docs/API.md) + SPA serving
internal/config      RADARO_* env / .env loading, data dir
web/                 React + Vite dashboard; web/dist is committed and embedded
skills/radaro/       the agent skill (embedded; installed by `radaro skill install`)
.claude-plugin/      Claude Code plugin + marketplace manifests
```

## Commands

```bash
go test ./...                      # all tests; add -race when touching the pipeline
go vet ./... && gofmt -l .         # CI fails on either
make build                         # ./radaro (CGO_ENABLED=0, version from git)
make web                           # rebuild web/dist; commit it (CI checks that it is current)
./radaro --db /tmp/t.db demo --serve=false   # offline smoke test with synthetic data
scripts/e2e.sh                     # build, serve a temporary database, drive the CLI end to end
```

## Conventions

- **Tests.** Every package with logic has `_test.go` files. Server tests sign in with `signedIn(t, srv, email)`; tests that hash passwords call `auth.FastHashingForTests()`. Network code is tested against `httptest` servers: endpoints are package variables (sources) or struct fields (publish), so tests can point them at a local server. Tests never hit the real internet.
- **Schema changes.** Append a step to `migrations` in `internal/store/migrate.go` with the next version; never edit a released step. Use `rebuildTable` when a constraint changes (SQLite cannot `ALTER` one). Timestamps are fixed-width UTC strings (`store.Stamp`) so they sort lexically.
- **Reads and writes.** Plain reads go through `s.rdb` (a read-only pool, so they never wait for a scan's write); writes, and reads inside a write, go through `s.db`, the single writer.
- **New scan source.** Implement `sources.Source`, register it in `registry` (`internal/sources/source.go`) with its metadata and `configured` check, add its options to `sources.Options`, `config.Load` and `.env.example`, and add a test with a fake server.
- **New publishing platform.** Implement `publish.Publisher`, add it to `publish.Platforms` and `publish.New`, add a `connect` subcommand, update `skills/radaro/references/platforms.md` and the README table, and test it against a fake API, including the error envelope.
- **API changes.** Update `docs/API.md`, `internal/server` tests, and the typed client in `web/src/api.ts` together.
- **Errors.** Never include secrets (tokens, webhook URLs, passwords) in error messages or logs.
- **Style.** Match the surrounding code, keep comments sparse and explain *why*, and prefer the standard library. New dependencies need a clear reason.
