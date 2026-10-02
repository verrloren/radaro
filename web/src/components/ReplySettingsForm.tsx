import { useEffect, useId, useImperativeHandle, useState, type Ref } from "react";
import { replyApi, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { ReplySettings } from "../types";
import { ErrorLine, Loading } from "./Status";

export interface ReplySettingsFormHandle {
  save: () => Promise<ReplySettings>;
}

interface Props {
  projectId: number;
  expanded?: boolean;
  disabled?: boolean;
  onReady?: (settings: ReplySettings) => void;
  onDirty?: (dirty: boolean) => void;
  ref?: Ref<ReplySettingsFormHandle>;
}

export function ReplySettingsForm(props: Readonly<Props>) {
  const [rev, setRev] = useState(0);
  const settings = useAsync((s) => replyApi.settings(props.projectId, s), [props.projectId, rev]);
  if (!settings.data) return <>{settings.loading && <Loading label="Loading reply context" />}<ErrorLine error={settings.error} onRetry={() => setRev((v) => v + 1)} /></>;
  return <Fields key={props.projectId} {...props} initial={settings.data} />;
}

function Fields({ projectId, initial, expanded, disabled, onReady, onDirty, ref }: Readonly<Props & {initial: ReplySettings}>) {
  const id = useId();
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  useEffect(() => { onReady?.(initial); }, [initial, onReady]);
  const field = (key: keyof ReplySettings, text: string) => {
    setValue((v) => ({...v, [key]: text}));
    setSaved(false);
    onDirty?.(true);
  };
  const save = async () => {
    setBusy(true);
    setError(null);
    try {
      const settings = await replyApi.saveSettings(projectId, value);
      setValue(settings);
      setSaved(true);
      onReady?.(settings);
      onDirty?.(false);
      return settings;
    } catch (e) { setError(errorMessage(e)); throw e; }
    finally { setBusy(false); }
  };
  useImperativeHandle(ref, () => ({save}));
  return <details className="reply-settings" open={expanded || (!initial.brief.trim() && !initial.instructions?.trim())}>
    <summary>What to promote &amp; comment instructions</summary>
    <div className="setup-form">
      <p className="muted small">Saved for this project and used when preparing replies. Existing drafts change only when you regenerate them.</p>
      <div className="field">
        <label htmlFor={`${id}-brief`}>What to promote</label>
        <textarea id={`${id}-brief`} rows={5} maxLength={8000} value={value.brief} disabled={busy || disabled} onChange={(e) => field("brief", e.target.value)} placeholder="Product name and link, who it helps, what it does, verified benefits, pricing and your connection to it." />
        <p className="muted small">The model uses these facts when relevant to the post. Leave empty for replies without promotion.</p>
      </div>
      <div className="field">
        <label htmlFor={`${id}-instructions`}>Comment instructions</label>
        <textarea id={`${id}-instructions`} rows={4} maxLength={8000} value={value.instructions ?? ""} disabled={busy || disabled} onChange={(e) => field("instructions", e.target.value)} placeholder="What the comment should be based on, which points to address, examples to use, what to avoid, and whether to include a link or ask a question." />
      </div>
      <div className="field"><label htmlFor={`${id}-language`}>Reply language</label><input type="text" id={`${id}-language`} maxLength={100} value={value.language} disabled={busy || disabled} onChange={(e) => field("language", e.target.value)} /></div>
      <div className="field"><label htmlFor={`${id}-tone`}>Tone</label><input type="text" id={`${id}-tone`} maxLength={500} value={value.tone} disabled={busy || disabled} onChange={(e) => field("tone", e.target.value)} /></div>
      <ErrorLine error={error} />
      <div className="btn-row"><button type="button" className="btn ghost sm" disabled={busy || disabled} onClick={() => { void save().catch(() => {}); }}>{busy ? "Saving…" : "Save reply context"}</button>{saved && <span role="status">Saved</span>}</div>
    </div>
  </details>;
}
