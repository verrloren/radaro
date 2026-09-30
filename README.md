# Radaro

**Self-hosted social listening and publishing in one binary.** Track what people say about a keyword, brand or product across Hacker News, Reddit, Bluesky, Mastodon, Stack Overflow, RSS, X and YouTube. Radaro scores sentiment, clusters themes and shows it all in a dashboard. When you want to answer a thread or announce something, draft the post, approve it, and publish it to Reddit, Bluesky, Mastodon or Dev.to from the same binary. Run it on your laptop or on your own server for a team: every user signs in and sees only their own projects, keywords and accounts. There is no telemetry, and the SQLite database stays on the machine that runs it.

![Radaro dashboard](docs/dashboard.png)

- **One binary.** Go, pure-Go SQLite, dashboard embedded. No Python, Node or runtime to install.
- **Zero-config first run.** `radaro demo` works offline. Hacker News, Bluesky and Stack Overflow need no keys.
- **Incremental + backfill scanning.** Durable per-source cursors catch up new mentions and walk back through history without re-fetching.
- **Local analysis.** Sentiment (lexicon with negation, intensifiers and contrast) and theme clustering run without any model. An LLM (Anthropic, OpenAI-compatible or local Ollama) is optional.
- **Alerts.** Slack / generic webhook and SMTP email for new negative mentions, volume spikes and sentiment drops. Delivery is durable: failed alerts retry on the next scan.
- **Publishing with a human in the loop.** Drafts must be approved before `radaro publish` sends them. Replies go into the original thread, and engagement metrics are read back.
- **Projects and accounts per user.** Sign in with email and password (JWT sessions) in the dashboard or the CLI. A project holds any number of keywords and one account per platform, so two projects can post as two different Reddit accounts.
- **Agent-friendly CLI.** The CLI talks to the server as the signed-in user, and every read command supports `--json`, so a coding agent (Claude Code, Codex, …) can find opportunities and write drafts for you to approve.

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/verrloren/radaro/main/install.sh | sh
```

The script picks the right binary for your OS and CPU (Linux and macOS, amd64 and arm64), verifies its checksum, and installs it to `/usr/local/bin` or `~/.local/bin`. Other options:

- **Homebrew (macOS and Linux):**
  ```bash
  brew trust --formula verrloren/radaro/radaro   # Homebrew 6+ refuses untrusted third-party formulae; skip on older versions
  brew tap verrloren/radaro https://github.com/verrloren/radaro
  brew install verrloren/radaro/radaro
  ```
- **Windows or manual:** download an archive from [Releases](https://github.com/verrloren/radaro/releases) and check it against `checksums.txt`.
- **From source:** `go install github.com/verrloren/radaro/cmd/radaro@latest` (Go 1.25+).

## Quickstart

```bash
radaro demo                         # synthetic dataset → dashboard at http://127.0.0.1:8042
radaro register                     # create the first account (it becomes the admin) and sign in
radaro track "arch linux"           # live scan (Hacker News + Bluesky by default)
```

Open http://127.0.0.1:8042 and sign in with the same email and password. `radaro serve` starts the dashboard without the demo data. To build from source, run `make build`. To run it in Docker, use `docker compose up -d`.

The server keeps all state in one database in its data directory (`~/.local/share/radaro/radaro.db` on Linux, `~/Library/Application Support/radaro` on macOS, `%LocalAppData%\radaro` on Windows). Override the directory with `RADARO_HOME` or the database with `--db` / `RADARO_DB`. A `.env` there (or in the current directory) is loaded automatically. `radaro status` shows what is set up.

## Accounts and sign-in

Everything except `/login` and `/register` needs a signed-in user, in the dashboard and in the API. The first account created on a server becomes its admin and takes over any data the database held before accounts existed. After that, sign-up is closed unless the server runs with `RADARO_REGISTRATION=open`.

```bash
radaro register --server https://radaro.example.com   # or: radaro login
radaro whoami
radaro logout
```

The CLI keeps its session in your config directory (`~/.config/radaro/auth.json`, readable only by you; override with `RADARO_CONFIG_DIR`) and renews it automatically. Passwords are typed at a hidden prompt or piped with `--password-stdin`, never passed as flags. Sessions are a 15-minute JWT plus a 30-day refresh token that rotates on every use; a stolen refresh token that is replayed signs out every session of that user. Sign-in is rate limited.

Each user sees only their own projects, keywords, accounts, drafts and activity. Mentions belong to keywords, so two users tracking the same keyword share one scan. Source keys for RSS, X and YouTube apply to the whole server and only the admin can change them. The admin can list users and reset a password on the server: `radaro admin users`, `radaro admin reset-password --email <address>`.

## Use it from your coding agent

Radaro ships an [Agent Skill](skills/radaro/SKILL.md) that teaches Claude Code, Codex and other skill-aware agents the whole loop: find threads where your project is relevant, write a tailored draft per community, **stop for your approval**, publish only what you approved, and report the results.

```bash
radaro skill install                 # into ~/.claude/skills and/or ~/.codex/skills (whichever exist)
# or, in Claude Code:
/plugin marketplace add verrloren/radaro
/plugin install radaro@radaro
```

Then ask your agent something like *"promote my project https://github.com/me/thing with radaro"*. The skill forbids approving or publishing without your explicit "yes" per draft, and it never asks you to paste API keys into the chat: you connect accounts yourself in the dashboard (**Setup**) or with `radaro connect`.

## Commands

| Command | What it does |
|---|---|
| `radaro demo` | Load a bundled synthetic dataset and open the dashboard |
| `radaro track <keyword>` | Fetch new mentions, analyze, store, alert (`--sources`, `--limit`, `--pages`, `--project`) |
| `radaro backfill <keyword>` | Fetch older pages via saved cursors (`--pages`) |
| `radaro watch <keyword>` | Scan on an interval (`--every 900`, `--runs N`) |
| `radaro report [keyword]` | Sentiment, sources, top themes and quotes |
| `radaro serve` | Dashboard + JSON API (`--port 8042`, `--host 127.0.0.1`) |
| `radaro register`, `login`, `logout`, `whoami` | Create an account or sign in to a server (`--server`, `--email`, `--password-stdin`) |
| `radaro project list\|create\|rename\|delete\|report` | Your projects |
| `radaro project keywords\|add\|remove` | A project's keywords (`add` takes several) |
| `radaro project accounts\|bind\|unbind` | Which account a project publishes and scans with on each platform |
| `radaro export [keyword]` | Full records as JSON or CSV (`-f csv -o file.csv`) |
| `radaro sources` | Available sources and whether they are configured |
| `radaro test-alert` | Send a synthetic alert (`--transport webhook\|email`, `--kind negative\|volume\|sentiment`) |
| `radaro connect bluesky\|mastodon\|devto\|reddit` | Connect a publishing account (`--project` binds it to a project) |
| `radaro accounts` | List connected accounts (`accounts remove <id>`) |
| `radaro opportunities [keyword]` | Recent mentions that have no draft yet (`--days`, `--source`) |
| `radaro draft add\|list\|show\|edit\|approve\|skip` | Write and review posts and replies |
| `radaro publish <draft-id>` | Publish an approved draft |
| `radaro stats` | Engagement of everything published |
| `radaro activity` | Log of what was drafted, approved and published |
| `radaro status` | Server, user, configured sources, accounts, keywords, drafts |
| `radaro skill install\|show` | Install the agent skill into Claude Code / Codex |
| `radaro admin backup <path>` | Consistent snapshot of the database, safe while serving |
| `radaro admin users`, `admin reset-password` | Manage accounts on the server |

`serve`, `demo`, `test-alert` and `admin` run on the server and open its database directly. Every other command calls the server as the signed-in user.

Global flags: `--db <path>` for the server-side commands (default: `radaro.db` in the data directory, or `$RADARO_DB`) and `--json`.

## Sources

Set these up in the dashboard (`radaro serve` → **Setup**): keys are saved in the local database and apply to the next scan without a restart. Reddit and Mastodon scan with your connected account (see [Publishing](#publishing)), so one connection covers both scanning and posting. The `RADARO_*` variables below still work as a fallback; the dashboard wins.

| Source | Needs |
|---|---|
| Hacker News | nothing (Algolia API) |
| Bluesky | nothing (public AppView; the anonymous API serves only the latest page, so no deep backfill) |
| Stack Overflow | nothing (anonymous daily quota applies) |
| Reddit | a connected Reddit account, or `RADARO_REDDIT_CLIENT_ID` + `RADARO_REDDIT_CLIENT_SECRET`, or `RADARO_REDDIT_ACCESS_TOKEN` |
| Mastodon | a connected Mastodon account, or `RADARO_MASTODON_ACCESS_TOKEN` (+ `RADARO_MASTODON_INSTANCE`) |
| RSS / Atom | `RADARO_RSS_FEEDS` |
| X | `RADARO_X_BEARER_TOKEN` (API v2 recent search) |
| YouTube | `RADARO_YOUTUBE_API_KEY` |

A failing source never sinks a scan. Its error is recorded and the other sources still land. Every option is listed in [`.env.example`](.env.example).

## Alerts

Set `RADARO_WEBHOOK_URL` (a Slack incoming webhook or any HTTP endpoint), or set `RADARO_EMAIL_TO`, `RADARO_EMAIL_FROM` and `RADARO_SMTP_HOST`. On each scan:

- newly ingested **negative mentions** are batched per target;
- optional thresholds fire on a **volume spike** (`RADARO_ALERT_VOLUME_MULTIPLIER`) or a **net-sentiment drop** (`RADARO_ALERT_SENTIMENT_DROP`), compared with the preceding windows, with a cooldown.

Delivery state lives in SQLite, so nothing is sent twice and failures are retried. Check your setup with `radaro test-alert`.

## Publishing

Radaro talks to each platform's API directly. You connect your own accounts; credentials stay in the server's database (readable only by the server's user) and are never sent back to the browser or the CLI.

The easiest way is the dashboard: **Setup** → **Connect account** next to the platform, or **Connect new** in a project's Accounts block to bind it to that project at once. Each project publishes with its own account per platform, and a project's Reddit or Mastodon account is also used to scan that platform. Keys are checked with the platform before they are saved. For Reddit, create a "web app" at [reddit.com/prefs/apps](https://www.reddit.com/prefs/apps) with the redirect URI the form shows (`<your server>/oauth/reddit/callback`, e.g. `http://127.0.0.1:8042/oauth/reddit/callback`), then approve access on Reddit. The CLI commands below do the same from a terminal.

| Platform | Connect | Posts | Replies | Metrics |
|---|---|---|---|---|
| Bluesky | `radaro connect bluesky --handle you.bsky.social` + an [app password](https://bsky.app/settings/app-passwords) | ✓ (links and hashtags are clickable) | ✓ | likes, reposts, replies, quotes |
| Mastodon | `radaro connect mastodon --instance mastodon.social` + an access token (Preferences → Development, scopes `read write:statuses`) | ✓ | ✓ (remote threads are resolved) | favourites, reblogs, replies |
| Reddit | create an app at [reddit.com/prefs/apps](https://www.reddit.com/prefs/apps) with redirect `<your server>/oauth/reddit/callback`, then `radaro connect reddit --client-id <id>` and approve in the browser | ✓ self posts | ✓ posts and comments | score, comments, upvote ratio |
| Dev.to | `radaro connect devto` + an API key (Settings → Extensions) | ✓ articles (`--community` = tags) | — | views, reactions, comments |

Secrets are read from a hidden prompt, or from stdin when piped (`echo "$TOKEN" | radaro connect devto`).

```bash
radaro project create "Launch"                 # → id 2
radaro project add 2 "your-product" "your product alternative"
radaro connect reddit --client-id <id> --project 2   # this project posts as this account
radaro track "your-product" --sources hackernews,bluesky,reddit
radaro opportunities --days 7                  # threads worth answering
radaro draft add --platform reddit --project 2 --mention <id> --body-file reply.md   # reply in that thread
radaro draft add --platform bluesky --body "Radaro 0.2 is out: https://…"
radaro draft show 1 && radaro draft approve 1  # nothing is sent before approval
radaro publish 1
radaro stats
```

Drafts go `draft → approved → publishing → published` (or `failed`, which you can approve again after fixing). A draft is claimed before the network call, so a crash cannot silently post it twice. Editing an approved draft sends it back to review. Follow each community's rules: automated self-promotion gets accounts banned.

## Running it on a server

Radaro stays one binary with one SQLite file; nothing else is needed on the server.

1. Run `radaro serve` as its own user with `RADARO_DB` on persistent storage (or `docker compose up -d`, which keeps it in a volume). It binds to `127.0.0.1:8042`.
2. Put a TLS reverse proxy in front of it; passwords and session cookies must not travel over plain HTTP. With [Caddy](https://caddyserver.com): `radaro.example.com { reverse_proxy 127.0.0.1:8042 }`. Radaro reads `X-Forwarded-Proto` and `X-Forwarded-For` from a proxy on the same machine or a private network (Docker), so cookies are `Secure` and rate limits apply per client.
3. Create your account (`radaro register --server https://radaro.example.com` or `/register`), then keep `RADARO_REGISTRATION=closed` (the default) unless others should sign up.
4. Back up: `radaro admin backup /backups/radaro-$(date +%F).db` from cron, or stream the database continuously with [Litestream](https://litestream.io).

Sessions are signed with `RADARO_JWT_SECRET` when set (at least 32 bytes, e.g. `openssl rand -base64 48`); otherwise Radaro creates a random secret and keeps it in the database. To move an existing single-user database to a server, stop Radaro, take `radaro admin backup`, copy the file, and register: the first account takes over its data.

## HTTP API

`radaro serve` exposes the JSON API the dashboard uses, documented in [docs/API.md](docs/API.md). Every endpoint except sign-in needs a session cookie or a `Bearer` token.

## Development

```bash
make test        # go test ./...
make web         # rebuild the React dashboard into web/dist (committed, embedded via go:embed)
make build       # ./radaro
npm --prefix web run dev   # dashboard dev server with /api proxied to 127.0.0.1:8042
```

## License

MIT, see [LICENSE](LICENSE).
