import { useCallback, useEffect, useRef, useState } from "react";
import { draftsApi, llmApi, replyApi, errorMessage } from "../api";
import { useAsync } from "../hooks";
import type { DraftDetail, Mention, ReplySettings } from "../types";
import { Modal } from "./ui/Modal";
import { ErrorLine, Loading } from "./Status";
import { LLMConnectionBadge } from "./LLMStatusCard";
import { RedditPostContent } from "./RedditPostContent";
import { ReplySettingsForm, type ReplySettingsFormHandle } from "./ReplySettingsForm";

interface Props {
  mention: Mention;
  projectId?: number;
  compose: boolean;
  onClose: () => void;
  onDraft: (draft: DraftDetail) => void;
}

export function RedditReplyDialog({ mention, projectId, compose, onClose, onDraft }: Readonly<Props>) {
  const [rev, setRev] = useState(0);
  const details = useAsync((s)=>replyApi.post(mention.id,projectId,s),[mention.id,projectId,rev]);
  const llm = useAsync((s)=>llmApi.status(s),[]);
  const [editing,setEditing] = useState(compose);
  const [draft,setDraft] = useState<DraftDetail | null>(null);
  const [body,setBody] = useState("");
  const [busy,setBusy] = useState(false);
  const [error,setError] = useState<string | null>(null);
  const [context, setContext] = useState<ReplySettings | null>(null);
  const [contextDirty, setContextDirty] = useState(false);
  const contextForm = useRef<ReplySettingsFormHandle>(null);
  const contextReady = useCallback((settings: ReplySettings) => setContext(settings), []);
  const started = useRef(false);
  const request = useRef<AbortController | null>(null);
  const pid = details.data?.project_id ?? projectId;
  const post = details.data?.post;
  const blocked = !!post && (!post.can_reply || post.removed);
  const immutable = draft?.status === "published" || draft?.status === "publishing";
  useEffect(()=>()=>request.current?.abort(),[]);
  useEffect(()=>{
    const existing=details.data?.draft;
    if(existing && !draft){setDraft(existing);setBody(existing.body);}
  },[details.data,draft]);

  const generate = async () => {
    started.current=true;
    setEditing(true);setBusy(true);setError(null);
    const ctrl=new AbortController();request.current=ctrl;
    try {
      if(contextDirty){
        if(!contextForm.current)throw new Error("Reply context is still loading. Try again when it is ready.");
        await contextForm.current.save();
        if(ctrl.signal.aborted)return;
      }
      const d=await replyApi.prepare(mention.id,pid,draft ? {generate:true,regenerate:true,draft_id:draft.id} : {generate:true},ctrl.signal);
      if(!ctrl.signal.aborted){setDraft(d);setBody(d.body);}
    } catch(e){if(!ctrl.signal.aborted)setError(errorMessage(e));}
    finally{if(!ctrl.signal.aborted)setBusy(false);}
  };
  useEffect(()=>{
    if(editing && post && llm.data && context && !contextDirty && !started.current && (details.data?.draft || context.brief.trim() || context.instructions?.trim())){
      started.current=true;
      if(llm.data.configured && !blocked && !details.data?.draft && !body.trim())void generate();
    }
  },[editing,post,llm.data,blocked,context,contextDirty]);

  const save = async () => {
    setBusy(true);setError(null);
    try {
      const d=draft ? (body===draft.body ? draft : await draftsApi.edit(draft.id,{body})) : await replyApi.prepare(mention.id,pid,{body});
      onDraft(d);
    }catch(e){setError(errorMessage(e));setBusy(false);}
  };
  return <Modal title={editing ? "Reply to Reddit post" : "Reddit post"} onClose={onClose}>
    <div className="reddit-reply-dialog">
      {details.loading && <Loading label="Loading Reddit post and rules" />}
      <ErrorLine error={details.error} onRetry={()=>setRev((v)=>v+1)} />
      {editing && pid && <ReplySettingsForm ref={contextForm} projectId={pid} onReady={contextReady} onDirty={setContextDirty} disabled={busy} />}
      {post ? <RedditPostContent post={post} /> : <article><h3>{mention.title}</h3><p className="reddit-post-body">{mention.text}</p></article>}
      {!editing && <button type="button" className="btn primary" disabled={blocked || details.loading} onClick={()=>setEditing(true)}>Reply</button>}
      {editing && <div className="reply-composer setup-form">
        <div className="section-head"><h3>Your reply</h3>{llm.data && <LLMConnectionBadge status={llm.data} />}</div>
        <ErrorLine error={llm.error} />
        {llm.data && !llm.data.configured && <p className="muted small">LLM is not configured. Write your reply below.</p>}
        {context && !context.brief.trim() && !context.instructions?.trim() && !draft && <p className="muted small">Add what to promote and your comment instructions above, then save. You can also prepare a reply without promotion.</p>}
        {blocked && <p className="error-line">This post is unavailable for replies.</p>}
        {immutable && <p role="status">This draft is {draft.status}. {draft.remote_url && <a className="link" href={draft.remote_url} target="_blank" rel="noopener noreferrer">View published reply</a>}</p>}
        <label htmlFor="reddit-reply-body">Comment</label>
        <textarea id="reddit-reply-body" rows={8} value={body} maxLength={40000} onChange={(e)=>setBody(e.target.value)} disabled={busy || immutable} placeholder={busy ? "Preparing your draft…" : "Write a helpful reply to this post…"} />
        <ErrorLine error={error} />
        {contextDirty && <p className="muted small" role="status">Preparing a draft will save your updated project context first.</p>}
        {details.loading && <p className="muted small" role="status">Waiting for the Reddit post to load before preparing a draft.</p>}
        {!details.loading && !context && <p className="muted small" role="status">Reply context must load first. If it failed, use retry in the context section above.</p>}
        <div className="btn-row">
          {llm.data?.configured && !immutable && <button type="button" className="btn ghost sm" disabled={busy || blocked || details.loading || !context} onClick={()=>void generate()}>{busy?"Preparing…":contextDirty ? draft ? "Save context & regenerate" : "Save context & prepare" : draft?"Regenerate draft":"Prepare with LLM"}</button>}
          <button type="button" className="btn primary sm" disabled={busy || !body.trim() || blocked} onClick={()=>void save()}>{busy ? "Preparing…" : immutable ? "Open draft" : "Save and review draft"}</button>
        </div>
        <p className="muted small">Nothing is sent until you approve the final draft.</p>
        {draft && !immutable && <p className="muted small">Regenerate replaces this draft's text using the saved project context and sends it back for review.</p>}
      </div>}
    </div>
  </Modal>;
}
