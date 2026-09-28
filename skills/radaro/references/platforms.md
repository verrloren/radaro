# Platforms

## Connecting accounts

The user runs these in their own terminal. Radaro asks for the secret in a hidden prompt; never collect secrets in the chat.

| Platform | Command | What the user needs |
|---|---|---|
| Bluesky | `radaro connect bluesky --handle you.bsky.social` | An app password: bsky.app → Settings → Privacy and security → App passwords |
| Mastodon | `radaro connect mastodon --instance mastodon.social` | An access token: Preferences → Development → New application, scopes `read` and `write:statuses` |
| Dev.to | `radaro connect devto` | An API key: Settings → Extensions → DEV Community API Keys |
| Reddit | `radaro connect reddit --client-id <id>` | An app at reddit.com/prefs/apps ("web app" or "installed app") with redirect URI `http://127.0.0.1:8765/callback`; the command opens the browser to approve access |

`radaro accounts --json` lists what is connected. `radaro sources --json` shows which scanning sources are configured. Scanning Reddit needs `RADARO_REDDIT_CLIENT_ID` and `RADARO_REDDIT_CLIENT_SECRET` in the environment or in the `.env` in Radaro's data directory, shown by `radaro status`.

## What each platform accepts

| Platform | New post | Reply | Limits | Notes |
|---|---|---|---|---|
| Reddit | `--community <subreddit> --title … --body …` (a self post) | `--mention <id>` or `--reply-to <post or comment URL>` | title ≤ 300, body ≤ 40,000 | Markdown. Rules differ per subreddit; many allow self-promotion only in weekly threads or at a ratio (about 1 in 10 posts). New or low-karma accounts get filtered. |
| Bluesky | `--body …` | `--reply-to <bsky.app post URL>` or `--mention <id>` | 300 characters | Links and #hashtags become clickable automatically. No title; one idea per post. |
| Mastodon | `--body …` | `--reply-to <status URL>` (any instance) or `--mention <id>` | 500 characters (most instances) | Hashtags drive discovery (2–4 of them, CamelCase for readability). Mastodon culture dislikes marketing tone. |
| Dev.to | `--title … --body-file article.md --community "tag1,tag2"` | not supported | up to 4 tags | Markdown article. Needs a real write-up (how it works, what you learned), not an ad. |
| Hacker News | — | — | — | No posting API. Draft the text in the chat, and the user posts it. "Show HN: <name> – <what it does>" + a first comment with context. |
| X | — | — | — | Not supported for publishing yet. |

## Where to look

- **Reddit:** the niche subreddits (e.g. r/selfhosted, r/linux, r/opensource, r/golang, r/webdev, r/SideProject, r/LocalLLaMA; pick by topic). Check each subreddit's sidebar and rules at `https://www.reddit.com/r/<name>/about/rules`.
- **Bluesky:** posts about the problem space, or replies to people asking for recommendations.
- **Mastodon:** hashtags for the niche (#selfhosted, #linux, #golang, #opensource, …).
- **Dev.to:** a technical article on the build or the release.
- **Stack Overflow** mentions: questions the product answers. Answer on Stack Overflow only if the user wants to, by hand; Radaro doesn't publish there. Use them to learn the wording people use.
