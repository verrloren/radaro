# Platforms

## Connecting accounts

The user connects accounts themselves, never through the chat. The simplest way is the dashboard: a project's **Accounts** block → **Connect new** (or **Setup → Connect account**); For Reddit, **Login and password** starts an isolated browser on the server; the user completes CAPTCHA or 2FA in the interactive view. An optional per-account proxy applies to login, scans and publishing. Chromium must be installed (it is included in the Docker image). The **OAuth app** tab keeps the API flow available. Or they run these in their own terminal, where Radaro asks for the secret in a hidden prompt. Add `--project <id>` to add the new account to that project's pool.

| Platform | Command | What the user needs |
|---|---|---|
| Bluesky | `radaro connect bluesky --handle you.bsky.social` | An app password: bsky.app → Settings → Privacy and security → App passwords |
| Mastodon | `radaro connect mastodon --instance mastodon.social` | An access token: Preferences → Development → New application, scopes `read` and `write:statuses` |
| Dev.to | `radaro connect devto` | An API key: Settings → Extensions → DEV Community API Keys |
| Reddit | `radaro connect reddit --client-id <id>` | An app at reddit.com/prefs/apps ("web app" or "installed app") with the redirect URI the command prints (`<server>/oauth/reddit/callback`); the command opens the browser to approve access and waits |

For more accounts, the user can import a CSV or JSON list in **Setup → Import** or run `radaro connect import <file|->`. CSV has an optional `platform,handle,secret,instance` header; JSON is an array of objects with those fields. The server checks up to three rows at a time, saves valid accounts, and reports each row without its secret. Reddit accounts are connected individually in the dashboard. For OAuth, after one Reddit account is connected, **Add another Reddit account** or `radaro connect reddit` reuses its app credentials. The user should sign in to the other Reddit account in a private browser window before approving. An admin can configure one outbound proxy in Setup for platform access; without one, the server uses its standard HTTP proxy environment variables.

`radaro accounts --json` lists the user's connected accounts with each one's health, limits and quota. A project pools any number of accounts per platform: `radaro project accounts <id> --json` shows the pools, `radaro project bind <id> <platform> <account-id>` adds an account, and `radaro project unbind <id> <platform> [<account-id>]` removes one (or empties the pool, so the project uses any of the user's accounts on that platform). `radaro sources --json` shows which scanning sources are configured. A project's Reddit or Mastodon account also scans that platform. Keys for the other scanning sources (RSS, X, YouTube) apply to the whole server; the server's admin sets them in the dashboard under **Setup** or as `RADARO_*` variables.

## What each platform accepts

| Platform | New post | Reply | Limits | Notes |
|---|---|---|---|---|
| Reddit | `--community <subreddit> --title … --body …` (a self post) | `--mention <id>` or `--reply-to <post or comment URL>` | title ≤ 300, body ≤ 40,000 | Markdown. Rules differ per subreddit; many allow self-promotion only in weekly threads or at a ratio (about 1 in 10 posts). New or low-karma accounts get filtered. |
| Bluesky | `--body …` | `--reply-to <bsky.app post URL>` or `--mention <id>` | 300 characters | Links and #hashtags become clickable automatically. No title; one idea per post. |
| Mastodon | `--body …` | `--reply-to <status URL>` (any instance) or `--mention <id>` | 500 characters (most instances) | Hashtags drive discovery (2–4 of them, CamelCase for readability). Mastodon culture dislikes marketing tone. |
| Dev.to | `--title … --body-file article.md --community "tag1,tag2"` | not supported | up to 4 tags | Markdown article. Needs a real write-up (how it works, what you learned), not an ad. |
| Hacker News | — | — | — | No posting API. Draft the text in the chat, and the user posts it. "Show HN: <name> – <what it does>" + a first comment with context. |
| X | — | — | — | Not supported for publishing yet. |

## Publishing limits

Every account has self-imposed limits; Radaro refuses a publish that would break them and sends nothing. The defaults per account:

| Platform | Per rolling 24 hours | Between two publications | Between two posts in one community |
|---|---|---|---|
| Reddit | 5 | 10 minutes | 24 hours (subreddit) |
| Bluesky | 20 | 2 minutes | — |
| Mastodon | 20 | 2 minutes | — |
| Dev.to | 2 | 1 hour | — |

The user can change them per account with `radaro accounts limits <id> --daily N --interval 10m --cooldown 24h` (0 restores the default; the cooldown is whole hours). The community cooldown also applies across the user's accounts on the same platform, only one account may take part in a thread, and a post removed from one account is never re-published from another. When the platform itself rate-limits an account, it is marked `limited` until the time the platform gave, and the draft goes back to `approved`. `quota.next_at` in `radaro accounts --json` says when each account may publish next; a refused `radaro publish <id> --json` prints `next_at` too.

A draft without `--account` is published by an account picked at publish time from the project's pool (or any of the user's accounts on the platform when the pool is empty): live or unchecked, not paused, not rate-limited, within its limits, the one with the most quota left, then the least recently used.

## Where to look

- **Reddit:** the niche subreddits (e.g. r/selfhosted, r/linux, r/opensource, r/golang, r/webdev, r/SideProject, r/LocalLLaMA; pick by topic). Check each subreddit's sidebar and rules at `https://www.reddit.com/r/<name>/about/rules`.
- **Bluesky:** posts about the problem space, or replies to people asking for recommendations.
- **Mastodon:** hashtags for the niche (#selfhosted, #linux, #golang, #opensource, …).
- **Dev.to:** a technical article on the build or the release.
- **Stack Overflow** mentions: questions the product answers. Answer on Stack Overflow only if the user wants to, by hand; Radaro doesn't publish there. Use them to learn the wording people use.
