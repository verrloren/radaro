import { useId, useState } from "react";
import { replyApi, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { ReplySettings } from "../types";
import { ErrorLine } from "./Status";

export function ReplySettingsForm({ projectId }: Readonly<{projectId: number}>) {
  const settings = useAsync((s) => replyApi.settings(projectId, s), [projectId]);
  if (!settings.data) return <ErrorLine error={settings.error} />;
  return <Fields key={projectId} projectId={projectId} initial={settings.data} />;
}

function Fields({ projectId, initial }: Readonly<{projectId: number; initial: ReplySettings}>) {
  const id = useId();
  const [value, setValue] = useState(initial);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [saved, setSaved] = useState(false);
  const field = (key: keyof ReplySettings, text: string) => { setValue((v)=>({...v,[key]:text})); setSaved(false); };
  const save = async () => {
    setBusy(true); setError(null);
    try { await replyApi.saveSettings(projectId, value); setSaved(true); }
    catch(e) { setError(errorMessage(e)); }
    finally { setBusy(false); }
  };
  return <details className="reply-settings"><summary>Project context for replies</summary>
    <div className="setup-form">
      <div className="field"><label htmlFor={`${id}-brief`}>Project brief</label><textarea id={`${id}-brief`} rows={4} maxLength={8000} value={value.brief} onChange={(e)=>field("brief",e.target.value)} placeholder="What the project does, who it helps, verified facts and links." /></div>
      <div className="field"><label htmlFor={`${id}-language`}>Reply language</label><input id={`${id}-language`} maxLength={100} value={value.language} onChange={(e)=>field("language",e.target.value)} /></div>
      <div className="field"><label htmlFor={`${id}-tone`}>Tone</label><input id={`${id}-tone`} maxLength={500} value={value.tone} onChange={(e)=>field("tone",e.target.value)} /></div>
      <ErrorLine error={error} /><div className="btn-row"><button type="button" className="btn ghost sm" disabled={busy} onClick={()=>void save()}>{busy?"Saving…":"Save project context"}</button>{saved && <span role="status">Saved</span>}</div>
    </div>
  </details>;
}
