import type { RedditPostDetails } from "../types";
import { fmtDateTime, fmtNum } from "../format";

export function RedditPostContent({ post }: Readonly<{post: RedditPostDetails}>) {
  return <article className="reddit-post-content">
    <p className="muted small">r/{post.community || "—"} · u/{post.author || "[deleted]"}{post.created_at ? ` · ${fmtDateTime(post.created_at)}` : ""}</p>
    <h3>{post.title}</h3>
    <div className="reddit-post-stats">
      <span>Score: <b>{post.score === null ? "—" : fmtNum(post.score)}</b></span>
      <span>Comments: <b>{post.comments === null ? "—" : fmtNum(post.comments)}</b></span>
      {post.upvote_ratio !== null && <span><b>{Math.round(post.upvote_ratio * 100)}%</b> upvoted</span>}
      {post.flair && <span className="tag">{post.flair}</span>}
      {post.locked && <span className="tag">Locked</span>}
      {post.archived && <span className="tag">Archived</span>}
      {post.removed && <span className="tag">Removed / deleted</span>}
      {post.nsfw && <span className="tag">NSFW</span>}
    </div>
    {post.body && <p className="reddit-post-body">{post.body}</p>}
    <p className="muted small">Fetched: {fmtDateTime(post.fetched_at)} · <a className="link" href={post.url} target="_blank" rel="noopener noreferrer">Open on Reddit</a></p>
    {post.replies.length > 0 && <details><summary>Loaded comments ({post.replies.length})</summary><div className="reddit-comments">{post.replies.map((c,i) => <article key={c.url || i}>
      <p className="muted small">u/{c.author || "[deleted]"} · Score: {c.score === null ? "—" : fmtNum(c.score)} {c.url && <a className="link" href={c.url} target="_blank" rel="noopener noreferrer">View comment</a>}</p>
      <p className="reddit-post-body">{c.body}</p>
    </article>)}</div></details>}
    {post.rules.length > 0 && <details><summary>Subreddit rules ({post.rules.length})</summary>{post.rules.map((rule,i)=><div key={i}><h4>{rule.name}</h4><p className="reddit-post-body">{rule.description}</p></div>)}</details>}
  </article>;
}
