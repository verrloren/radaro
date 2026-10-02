import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { llmApi, replyApi, ApiError } from "../api";
import { RedditReplyDialog } from "../components/RedditReplyDialog";
import { LLMStatusCard } from "../components/LLMStatusCard";
import type { Mention, RedditPostDetails } from "../types";
import { draft } from "./fixtures";

const mention: Mention={id:"abc",source:"reddit",source_label:"Reddit",glyph:"R",color:"red",query:"linux",author:"bob",title:"Linux question",text:"Snippet",url:"https://www.reddit.com/r/linux/comments/abc/question/",created_at:"2026-10-01T12:00:00Z",score:0,sentiment:null,sentiment_score:null,theme:null};
const post: RedditPostDetails={url:mention.url!,title:mention.title!,body:"Full post text",author:"bob",community:"linux",created_at:mention.created_at,score:0,comments:0,upvote_ratio:null,flair:"Question",locked:false,archived:false,removed:false,nsfw:false,can_reply:true,replies:[],rules:[],fetched_at:mention.created_at};
const status={provider:"none",model:"",configured:false,status:"not_configured" as const,checked_at:null,detail:"LLM is not configured."};

beforeEach(()=>{
  vi.spyOn(replyApi,"post").mockResolvedValue({post,project_id:3});
  vi.spyOn(replyApi,"settings").mockResolvedValue({brief:"",language:"Same as the post",tone:"Helpful"});
  vi.spyOn(llmApi,"status").mockResolvedValue(status);
});

describe("Reddit reply preparation",()=>{
  it("reads the full post, preserves zero statistics, and saves a manual draft without an LLM",async()=>{
    const d=draft({kind:"reply",project_id:3});const prepare=vi.spyOn(replyApi,"prepare").mockResolvedValue(d);const onDraft=vi.fn();
    render(<RedditReplyDialog mention={mention} compose projectId={3} onClose={vi.fn()} onDraft={onDraft} />);
    expect(await screen.findByText("Full post text")).toBeTruthy();
    expect(screen.queryByText(/% upvoted/)).toBeNull();
    expect(screen.getByText("Score:").textContent).toContain("0");
    expect(screen.getByText("Comments:").textContent).toContain("0");
    expect(prepare).not.toHaveBeenCalled();
    await userEvent.type(screen.getByLabelText("Comment"),"Manual answer");
    await userEvent.click(screen.getByRole("button",{name:"Save and review draft"}));
    expect(prepare).toHaveBeenCalledWith("abc",3,{body:"Manual answer"});expect(onDraft).toHaveBeenCalledWith(d);
  });
  it("automatically prepares one draft after Reply, then opens the saved draft",async()=>{
    vi.spyOn(llmApi,"status").mockResolvedValue({...status,provider:"openai",configured:true,status:"unchecked"});
    const d=draft({kind:"reply",body:"Generated answer"});const prepare=vi.spyOn(replyApi,"prepare").mockResolvedValue(d);const onDraft=vi.fn();
    render(<RedditReplyDialog mention={mention} compose onClose={vi.fn()} onDraft={onDraft} />);
    await waitFor(()=>expect((screen.getByLabelText("Comment") as HTMLTextAreaElement).value).toBe("Generated answer"));
    expect(prepare).toHaveBeenCalledTimes(1);expect(prepare.mock.calls[0][2]).toEqual({generate:true});
    await userEvent.click(screen.getByRole("button",{name:"Save and review draft"}));
    expect(onDraft).toHaveBeenCalledWith(d);expect(prepare).toHaveBeenCalledTimes(1);
  });
  it("opens an existing draft without regenerating it",async()=>{
    const d=draft({kind:"reply",body:"Already edited"});
    vi.spyOn(replyApi,"post").mockResolvedValue({post,project_id:3,draft:d});
    vi.spyOn(llmApi,"status").mockResolvedValue({...status,configured:true,status:"connected"});
    const prepare=vi.spyOn(replyApi,"prepare");
    render(<RedditReplyDialog mention={mention} compose onClose={vi.fn()} onDraft={vi.fn()} />);
    await waitFor(()=>expect((screen.getByLabelText("Comment") as HTMLTextAreaElement).value).toBe("Already edited"));
    expect(prepare).not.toHaveBeenCalled();
  });
  it("keeps manual text after a preparation failure",async()=>{
    vi.spyOn(llmApi,"status").mockResolvedValue({...status,configured:true,status:"connected"});
    vi.spyOn(replyApi,"prepare").mockRejectedValue(new ApiError(502,"Provider unavailable"));
    render(<RedditReplyDialog mention={mention} compose onClose={vi.fn()} onDraft={vi.fn()} />);
    expect(await screen.findByText("Provider unavailable")).toBeTruthy();
    await userEvent.type(screen.getByLabelText("Comment"),"My answer");
    await userEvent.click(screen.getByRole("button",{name:"Prepare with LLM"}));
    await waitFor(()=>expect((screen.getByLabelText("Comment") as HTMLTextAreaElement).disabled).toBe(false));
    expect((screen.getByLabelText("Comment") as HTMLTextAreaElement).value).toBe("My answer");
  });
});

describe("LLM connection status",()=>{
  it("shows unconfigured status and disables the connection check",async()=>{
    render(<LLMStatusCard isAdmin />);
    expect(await screen.findByText("LLM · Not configured")).toBeTruthy();
    expect((screen.getByRole("button",{name:"Check connection"}) as HTMLButtonElement).disabled).toBe(true);
  });
  it("shows actual check errors separately from configuration",async()=>{
    vi.spyOn(llmApi,"status").mockResolvedValue({...status,configured:true,status:"unchecked"});
    vi.spyOn(llmApi,"check").mockResolvedValue({...status,configured:true,status:"error",detail:"Request failed"});
    render(<LLMStatusCard isAdmin />);
    await userEvent.click(await screen.findByRole("button",{name:"Check connection"}));
    expect(await screen.findByText("LLM · Connection error")).toBeTruthy();
  });
});
