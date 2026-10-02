import { useId, useState } from "react";
import { draftsApi, errorMessage, retryAt } from "../api";
import { useAsync, type AsyncState } from "../hooks";
import { ago, fmtDateTime, fmtNum } from "../format";
import type { Account, AccountsResponse, DraftDetail, DraftEdit, DraftStatus, DraftWarning, Platform, PublishPlan } from "../types";
import { draftWhere, platformBadge, StatusBadge } from "./DraftsList";
import { SourceIcon, type BadgeSource } from "./SourceBadge";
import { ErrorLine, Loading } from "./Status";
import { Modal } from "./ui/Modal";
import { Select } from "./ui/Select";

const REVIEWABLE = new Set(["draft", "approved", "failed"]);
const RULE_CODES = new Set(["subreddit_rules", "community_bans_promo"]);
const BANS_PROMO = "community_bans_promo";
const REDDIT_TITLE_MAX = 300;

type Notice = { text: string; url?: string };

interface Props {
  id: number;
  rev: number;
  accounts: AccountsResponse | undefined;
  onChanged: () => void;
  onNotice: (notice: Notice) => void;
}

/** One draft: its text, account and limits, the advice for it, and the review actions. */
export function DraftsEditor({ id, rev, accounts, onChanged, onNotice }: Readonly<Props>) {
  const detail = useAsync((s) => draftsApi.get(id, s), [id, rev]);
  const d = detail.data;
  const reviewable = d !== undefined && REVIEWABLE.has(d.status);
  const warnings = useAsync((s) => draftsApi.warnings(id, s), [id, rev], reviewable);

  if (!d) {
    return (
      <section className="card pad draft-editor">
        {detail.loading && <Loading label="Loading draft" />}
        <ErrorLine error={detail.error} onRetry={onChanged} />
      </section>
    );
  }
  return (
    <DraftForm
      key={`${d.id}:${d.updated_at}`}
      d={d}
      accounts={accounts}
      warnings={reviewable ? warnings : undefined}
      onChanged={onChanged}
      onNotice={onNotice}
    />
  );
}

/** Characters as the server counts them: code points of the trimmed text. */
function chars(s: string): number {
  return [...s.trim()].length;
}

/** "14:20" today, a date and time otherwise, "now" when it has passed. */
function when(iso: string): string {
  const t = new Date(iso);
  if (Number.isNaN(t.getTime()) || t.getTime() <= Date.now()) return "now";
  if (t.toDateString() === new Date().toDateString()) return `at ${t.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit" })}`;
  return `on ${fmtDateTime(iso)}`;
}

function accountState(a: Account): string {
  if (a.paused) return "paused";
  if (a.status === "limited" && a.limited_until) return `limited until ${fmtDateTime(a.limited_until)}`;
  return a.status === "unknown" ? "not checked yet" : a.status;
}

const accountValue = (id: number | null) => (id === null ? "" : String(id));

const kindLabel = (d: DraftDetail) => (d.kind === "reply" ? "reply" : "post");

function approveLabel(status: DraftStatus): string {
  if (status === "failed") return "Approve retry";
  if (status === "skipped") return "Restore to review";
  return "Approve";
}

function planTone(plan: PublishPlan): string {
  if (plan.account && !plan.reason) return "tone-ok";
  return plan.next_at ? "tone-setup" : "tone-error";
}

function planWhen(plan: PublishPlan, handle: string | undefined): string {
  if (plan.next_at) return `can publish ${when(plan.next_at)}`;
  if (plan.reason || plan.quota?.ready === false) return "needs attention before publishing";
  return handle ? "can publish now" : "";
}

/** Stable across renders: the server sends each warning once. */
const warningKey = (w: DraftWarning) => `${w.code}:${w.text}`;

/** The editable fields of a draft, and what changed against the saved one. */
function useDraftEdit(d: DraftDetail, showTitle: boolean, showCommunity: boolean) {
  const [title, setTitle] = useState(d.title ?? "");
  const [body, setBody] = useState(d.body);
  const [community, setCommunity] = useState(d.community ?? "");
  const [account, setAccount] = useState(accountValue(d.account_id));

  const edit: DraftEdit = {};
  if (showTitle && title !== (d.title ?? "")) edit.title = title;
  if (body !== d.body) edit.body = body;
  if (showCommunity && community !== (d.community ?? "")) edit.community = community;
  if (account !== accountValue(d.account_id)) edit.account_id = account === "" ? null : Number(account);

  const reset = () => {
    setTitle(d.title ?? "");
    setBody(d.body);
    setCommunity(d.community ?? "");
    setAccount(accountValue(d.account_id));
  };

  return { title, setTitle, body, setBody, community, setCommunity, account, setAccount, edit, dirty: Object.keys(edit).length > 0, reset };
}

type DraftEditState = ReturnType<typeof useDraftEdit>;

interface FormProps {
  d: DraftDetail;
  accounts: AccountsResponse | undefined;
  warnings: AsyncState<DraftWarning[]> | undefined;
  onChanged: () => void;
  onNotice: (notice: Notice) => void;
}

/** What the editor offers for a draft on its platform. */
function draftLayout(d: DraftDetail, accounts: AccountsResponse | undefined) {
  const platforms: Platform[] = accounts?.platforms ?? [];
  const platform = platforms.find((p) => p.name === d.platform);
  return {
    badge: platformBadge(d.platform, platforms),
    own: (accounts?.accounts ?? []).filter((a) => a.platform === d.platform),
    maxChars: platform?.max_chars ?? 0,
    editable: REVIEWABLE.has(d.status) || d.status === "skipped",
    showTitle: d.kind === "post" && (platform?.titles ?? d.title !== null),
    showCommunity: d.platform === "devto" || (d.platform === "reddit" && (d.kind === "post" || d.community !== null)),
  };
}

/** A limit refuses a publish with the time it may go out; say it in local time. */
function describeError(e: unknown): string {
  const next = retryAt(e);
  const msg = errorMessage(e);
  return next ? `${msg} It can go out ${when(next)}.` : msg;
}

/** Runs one review action at a time and keeps its error. */
function useAction(onChanged: () => void) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  const run = async (fn: () => Promise<unknown>, after?: () => void) => {
    setBusy(true);
    setError(null);
    try {
      await fn();
      after?.();
    } catch (e) {
      setError(describeError(e));
    }
    setBusy(false);
    // Refresh either way: a refused publish may have changed the draft or the limits.
    onChanged();
  };

  return { busy, error, run };
}

function DraftForm({ d, accounts, warnings, onChanged, onNotice }: Readonly<FormProps>) {
  const uid = useId();
  const { badge, own, maxChars, editable, showTitle, showCommunity } = draftLayout(d, accounts);
  const form = useDraftEdit(d, showTitle, showCommunity);
  const { busy, error, run } = useAction(onChanged);
  const [confirm, setConfirm] = useState(false);

  const publish = () =>
    run(
      async () => {
        if (d.status !== "approved") await draftsApi.approve(d.id);
        const res = await draftsApi.publish(d.id);
        onNotice({ text: `Published draft ${d.id} as ${res.account.handle}.`, url: res.draft.remote_url ?? undefined });
      },
      () => setConfirm(false),
    );

  // A skipped draft goes back to review the way any edit does.
  const approve = () => run(() => (d.status === "skipped" ? draftsApi.edit(d.id, { body: d.body }) : draftsApi.approve(d.id)));

  const all = warnings?.data ?? [];
  const rules = all.filter((w) => RULE_CODES.has(w.code));
  const bansPromo = rules.some((w) => w.code === BANS_PROMO);
  const handle = d.plan?.account?.handle;

  return (
    <section className="card pad draft-editor" aria-labelledby={`${uid}-h`}>
      <DraftHead d={d} badge={badge} headingId={`${uid}-h`} />
      <DraftState d={d} badge={badge} />

      <form
        className="setup-form draft-form"
        onSubmit={(e) => {
          e.preventDefault();
          if (form.dirty) void run(() => draftsApi.edit(d.id, form.edit));
        }}
      >
        <DraftFields
          uid={uid}
          d={d}
          form={form}
          maxChars={maxChars}
          showTitle={showTitle}
          showCommunity={showCommunity}
          disabled={!editable || busy}
        />
        {editable && (
          <AccountField uid={uid} value={form.account} onChange={form.setAccount} own={own} platformLabel={badge.label} disabled={busy} />
        )}
        {d.plan && !form.dirty && <PlanLine plan={d.plan} auto={d.account_id === null} />}
        {warnings && <DraftAdvice warnings={warnings} rules={rules} bansPromo={bansPromo} />}

        <ErrorLine error={error} />

        {editable && (
          <DraftActions
            status={d.status}
            approveAndPublish={d.platform === "reddit" && d.kind === "reply"}
            dirty={form.dirty}
            busy={busy}
            onDiscard={form.reset}
            onApprove={() => void approve()}
            onPublish={() => setConfirm(true)}
            onSkip={() => void run(() => draftsApi.skip(d.id))}
          />
        )}
      </form>

      {confirm && (
        <PublishConfirm
          d={d}
          badge={badge}
          handle={handle}
          bansPromo={bansPromo}
          busy={busy}
          error={error}
          onPublish={() => void publish()}
          onCancel={() => setConfirm(false)}
        />
      )}
    </section>
  );
}

function NewTab() {
  return <span className="sr-only"> (opens in a new tab)</span>;
}

function DraftHead({ d, badge, headingId }: Readonly<{ d: DraftDetail; badge: BadgeSource; headingId: string }>) {
  return (
    <header className="draft-head">
      <SourceIcon source={badge} size={28} />
      <span className="draft-head-text">
        <h2 id={headingId} className="card-title">
          {badge.label} {kindLabel(d)}
          <span className="muted"> · #{d.id}</span>
        </h2>
        <span className="muted small">
          {draftWhere(d)} · created {ago(d.created_at)}
          {d.query && (
            <>
              {" "}
              · promotes <span className="strong">{d.query}</span>
            </>
          )}
        </span>
      </span>
      <StatusBadge d={d} />
    </header>
  );
}

/** Where the draft stands: what it replies to, and how its publication went. */
function DraftState({ d, badge }: Readonly<{ d: DraftDetail; badge: BadgeSource }>) {
  return (
    <>
      {d.reply_to && (
        <p className="small draft-reply">
          Replying to{" "}
          <a className="link" href={d.reply_to} target="_blank" rel="noopener noreferrer">
            {d.reply_to}
            <NewTab />
          </a>
        </p>
      )}
      {d.status === "published" && <PublishedPanel d={d} badge={badge} />}
      {d.status === "failed" && d.error && <ErrorLine error={`Last attempt failed: ${d.error}`} />}
      {d.status === "publishing" && (
        <p className="muted small">
          Being published. If this does not change, the attempt was interrupted; check the platform before retrying.
        </p>
      )}
    </>
  );
}

function PublishedPanel({ d, badge }: Readonly<{ d: DraftDetail; badge: BadgeSource }>) {
  const metrics = Object.entries(d.metrics ?? {}).filter(([k]) => k !== "removed");
  return (
    <div className="draft-published">
      <p className="ok-line">
        ✓ Published {d.published_at ? ago(d.published_at) : ""}
        {d.remote_url && (
          <>
            {" · "}
            <a className="link" href={d.remote_url} target="_blank" rel="noopener noreferrer">
              View on {badge.label}
              <NewTab />
            </a>
          </>
        )}
      </p>
      {d.removed_at && (
        <output className="draft-removed small">Removed by the platform or its moderators, noticed {ago(d.removed_at)}.</output>
      )}
      <p className="draft-metrics small">
        {metrics.length ? (
          metrics.map(([k, v]) => (
            <span key={k} className="tag">
              {k} <span className="num">{typeof v === "number" ? fmtNum(v) : String(v)}</span>
            </span>
          ))
        ) : (
          <span className="muted">No metrics yet.</span>
        )}
        {d.metrics_at && <span className="muted"> updated {ago(d.metrics_at)}</span>}
      </p>
    </div>
  );
}

interface FieldsProps {
  uid: string;
  d: DraftDetail;
  form: DraftEditState;
  maxChars: number;
  showTitle: boolean;
  showCommunity: boolean;
  disabled: boolean;
}

function DraftFields({ uid, d, form, maxChars, showTitle, showCommunity, disabled }: Readonly<FieldsProps>) {
  const titleN = chars(form.title);
  const n = chars(form.body);
  const reddit = d.platform === "reddit";
  return (
    <>
      {showTitle && (
        <div className="field">
          <label htmlFor={`${uid}-title`} className="small strong draft-label">
            Title
            {reddit && (
              <span className={`num draft-count${titleN > REDDIT_TITLE_MAX ? " is-over" : ""}`}>
                {titleN}/{REDDIT_TITLE_MAX}
              </span>
            )}
          </label>
          <input id={`${uid}-title`} type="text" value={form.title} onChange={(e) => form.setTitle(e.target.value)} disabled={disabled} />
        </div>
      )}
      {showCommunity && (
        <div className="field">
          <label htmlFor={`${uid}-community`} className="small strong">
            {reddit ? "Subreddit" : "Tags (comma-separated)"}
          </label>
          <input
            id={`${uid}-community`}
            type="text"
            value={form.community}
            placeholder={reddit ? "golang" : "go, opensource"}
            onChange={(e) => form.setCommunity(e.target.value)}
            disabled={disabled}
            spellCheck={false}
          />
        </div>
      )}
      <div className="field">
        <label htmlFor={`${uid}-body`} className="small strong draft-label">
          {"Text"}
          <span className={`num draft-count${maxChars > 0 && n > maxChars ? " is-over" : ""}`}>
            {maxChars > 0 ? `${fmtNum(n)}/${fmtNum(maxChars)}` : `${fmtNum(n)} characters`}
          </span>
        </label>
        <textarea
          id={`${uid}-body`}
          className="draft-body"
          rows={Math.min(18, Math.max(6, form.body.split("\n").length + 1))}
          value={form.body}
          onChange={(e) => form.setBody(e.target.value)}
          disabled={disabled}
        />
      </div>
    </>
  );
}

interface AccountFieldProps {
  uid: string;
  value: string;
  onChange: (value: string) => void;
  own: Account[];
  platformLabel: string;
  disabled: boolean;
}

function AccountField({ uid, value, onChange, own, platformLabel, disabled }: Readonly<AccountFieldProps>) {
  return (
    <div className="field">
      <label htmlFor={`${uid}-account`} className="small strong">
        Account
      </label>
      <Select
        id={`${uid}-account`}
        label="Account"
        value={value}
        onChange={onChange}
        disabled={disabled}
        options={[
          { value: "", label: "Auto — the best account when publishing" },
          ...own.map((a) => ({ value: String(a.id), label: `${a.handle} · ${accountState(a)}` })),
        ]}
      />
      {own.length === 0 && <span className="muted small">No {platformLabel} account is connected yet; connect one in Setup.</span>}
    </div>
  );
}

/** Who publishes the draft and when, as the limits stand now. */
function PlanLine({ plan, auto }: Readonly<{ plan: PublishPlan; auto: boolean }>) {
  const handle = plan.account?.handle;
  return (
    <output className="draft-plan small">
      <span className={`dot ${planTone(plan)}`} aria-hidden="true" />
      <span>
        {handle ? (
          <>
            {auto ? "Auto picks " : "Publishes as "}
            <span className="strong">{handle}</span>
            {plan.account && ` (${accountState(plan.account)})`}
          </>
        ) : (
          "No account can publish it right now"
        )}
        {plan.quota && ` · ${plan.quota.used_24h} of ${plan.quota.daily} used in the last 24 h`}
        {" · "}
        <span className="strong">{planWhen(plan, handle)}</span>
        {plan.reason && <span className="muted"> — {plan.reason}</span>}
      </span>
    </output>
  );
}

interface AdviceProps {
  warnings: AsyncState<DraftWarning[]>;
  rules: DraftWarning[];
  bansPromo: boolean;
}

function DraftAdvice({ warnings, rules, bansPromo }: Readonly<AdviceProps>) {
  const all = warnings.data ?? [];
  const advice = all.filter((w) => !RULE_CODES.has(w.code));
  return (
    <div className="draft-advice">
      {warnings.loading && !warnings.data && <Loading label="Checking the draft" />}
      {warnings.error && <p className="muted small">Could not check the draft: {warnings.error}</p>}
      {advice.length > 0 && (
        <ul className="warn-list" aria-label="Advice before publishing">
          {advice.map((w) => (
            <li key={warningKey(w)} className={`warn-item w-${w.code}`}>
              <span aria-hidden="true">!</span>
              <span>{w.text}</span>
            </li>
          ))}
        </ul>
      )}
      {rules.length > 0 && (
        <details className="draft-rules" open={bansPromo}>
          <summary className="small strong">
            Subreddit rules ({rules.length})
            {bansPromo && <span className="badge b-mari">restricts self-promotion</span>}
          </summary>
          <ul className="warn-list">
            {rules.map((w) => (
              <li key={warningKey(w)} className={`warn-item w-${w.code}`}>
                <span aria-hidden="true">{w.code === BANS_PROMO ? "!" : "·"}</span>
                <span>{w.text}</span>
              </li>
            ))}
          </ul>
        </details>
      )}
      {warnings.data && all.length === 0 && <p className="muted small">No advice for this draft.</p>}
    </div>
  );
}

interface ActionsProps {
  status: DraftStatus;
  dirty: boolean;
  busy: boolean;
  onDiscard: () => void;
  onApprove: () => void;
  onPublish: () => void;
  approveAndPublish?: boolean;
  onSkip: () => void;
}

function DraftActions({ status, dirty, busy, onDiscard, onApprove, onPublish, onSkip, approveAndPublish }: Readonly<ActionsProps>) {
  if (dirty) {
    return (
      <div className="btn-row draft-actions">
        <button type="submit" className="btn primary sm" disabled={busy}>
          Save changes
        </button>
        <button type="button" className="btn ghost sm" disabled={busy} onClick={onDiscard}>
          Discard
        </button>
        <span className="muted small">Saving sends it back to review.</span>
      </div>
    );
  }
  const approved = status === "approved";
  return (
    <div className="btn-row draft-actions">
      {!approved && (
        <button type="button" className="btn primary sm" disabled={busy} onClick={approveAndPublish && status !== "skipped" ? onPublish : onApprove}>
          {approveAndPublish && status !== "skipped" ? "Approve & publish…" : approveLabel(status)}
        </button>
      )}
      <button
        type="button"
        className={`btn sm${approved ? " primary" : ""}`}
        disabled={busy || !approved}
        title={approved ? undefined : "Approve the draft first"}
        onClick={onPublish}
      >
        Publish…
      </button>
      {status !== "skipped" && (
        <button type="button" className="btn ghost danger sm" disabled={busy} onClick={onSkip}>
          Skip
        </button>
      )}
    </div>
  );
}

interface ConfirmProps {
  d: DraftDetail;
  badge: BadgeSource;
  handle: string | undefined;
  bansPromo: boolean;
  busy: boolean;
  error: string | null;
  onPublish: () => void;
  onCancel: () => void;
}

function PublishConfirm({ d, badge, handle, bansPromo, busy, error, onPublish, onCancel }: Readonly<ConfirmProps>) {
  const later = d.plan?.next_at ? when(d.plan.next_at) : "now";
  return (
    <Modal title={d.status === "approved" ? "Publish this draft?" : "Approve and publish this draft?"} onClose={() => !busy && onCancel()}>
      <div className="setup-form">
        <p>
          This {kindLabel(d)} goes out publicly on <span className="strong">{badge.label}</span>
          {d.platform === "reddit" && d.community ? ` in ${draftWhere(d)}` : ""}
          {handle ? (
            <>
              {" "}
              as <span className="strong">{handle}</span>
            </>
          ) : (
            " from the account picked when publishing"
          )}
          . Radaro cannot remove it from the platform after publishing.
        </p>
        {d.reply_to && <p className="small">Replying to <a className="link" href={d.reply_to} target="_blank" rel="noopener noreferrer">{d.reply_to}</a></p>}
        {d.title && <h3>{d.title}</h3>}
        <div className="publish-preview reddit-post-body">{d.body}</div>
        {later !== "now" && <p className="muted small">The limits say it can go out {later}; publishing earlier will be refused.</p>}
        {bansPromo && <p className="muted small">This subreddit restricts self-promotion; make sure the post follows its rules.</p>}
        <ErrorLine error={error} />
        <div className="btn-row">
          <button type="button" className="btn primary sm" disabled={busy} onClick={onPublish}>
            {busy ? "Publishing…" : d.status === "approved" ? "Publish now" : "Approve & publish now"}
          </button>
          <button type="button" className="btn ghost sm" disabled={busy} onClick={onCancel}>
            Cancel
          </button>
        </div>
      </div>
    </Modal>
  );
}
