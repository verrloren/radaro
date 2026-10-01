import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api, ApiError } from "../api";
import { RedditBrowserForm } from "../components/RedditBrowserForm";
import { AccountProxyForm } from "../components/AccountProxyForm";
import { accountView } from "./fixtures";
const picture = { image: "cG5n", width: 1000, height: 720 };

describe("Reddit browser sign-in", () => {
  beforeEach(() => {
    vi.spyOn(api, "redditBrowser").mockResolvedValue({ available: true });
    vi.spyOn(api, "startRedditBrowser").mockResolvedValue({ session_id: "s1", screen: picture, expires_at: "2026-10-01T12:10:00Z" });
    vi.spyOn(api, "finishRedditBrowser").mockRejectedValue(new ApiError(409, "Complete Reddit verification"));
    vi.spyOn(api, "redditBrowserInput").mockResolvedValue(picture);
    vi.spyOn(api, "cancelRedditBrowser").mockResolvedValue({ cancelled: true });
  });
  it("passes login and per-account proxy, then lets the user complete verification", async () => {
    const user = userEvent.setup(); const done = vi.fn();
    const view = render(<RedditBrowserForm projectId={3} onDone={done} />);
    await user.type(await screen.findByLabelText("Reddit login or email"), "alice");
    await user.type(screen.getByLabelText("Reddit password"), "private-pass");
    await user.type(screen.getByLabelText(/Account proxy/), "socks5://u:private-proxy@host:1080");
    await user.click(screen.getByRole("button", { name: "Connect Reddit" }));
    expect(api.startRedditBrowser).toHaveBeenCalledWith({ username: "alice", password: "private-pass", proxy_url: "socks5://u:private-proxy@host:1080", project_id: 3 }, expect.any(AbortSignal));
    expect(await screen.findByLabelText("Interactive Reddit sign-in")).toBeTruthy();
    await waitFor(() => expect(api.finishRedditBrowser).toHaveBeenCalledWith("s1"));
    expect(done).not.toHaveBeenCalled();
    await user.type(screen.getByLabelText(/Text or verification code/), "123456");
    await user.click(screen.getByRole("button", { name: "Enter text" }));
    expect(api.redditBrowserInput).toHaveBeenCalledWith("s1", { kind: "text", text: "123456" });
    vi.mocked(api.finishRedditBrowser).mockResolvedValue(accountView());
    await user.click(screen.getByRole("button", { name: "Complete connection" }));
    await waitFor(() => expect(done).toHaveBeenCalledOnce());
    view.unmount(); expect(api.cancelRedditBrowser).not.toHaveBeenCalled();
  });
  it("cancels a pending browser when the dialog closes", async () => {
    const user = userEvent.setup(); const view = render(<RedditBrowserForm />);
    await user.type(await screen.findByLabelText("Reddit login or email"), "alice");
    await user.type(screen.getByLabelText("Reddit password"), "pass");
    await user.click(screen.getByRole("button", { name: "Connect Reddit" }));
    await screen.findByLabelText("Interactive Reddit sign-in");
    view.unmount(); expect(api.cancelRedditBrowser).toHaveBeenCalledWith("s1");
  });
  it("delivers rapid clicks in order while the previous screen is loading", async () => {
    const user = userEvent.setup(); render(<RedditBrowserForm />);
    await user.type(await screen.findByLabelText("Reddit login or email"), "alice");
    await user.type(screen.getByLabelText("Reddit password"), "pass");
    await user.click(screen.getByRole("button", { name: "Connect Reddit" }));
    const image = await screen.findByLabelText("Interactive Reddit sign-in");
    await waitFor(() => expect((screen.getByRole("button", { name: "Complete connection" }) as HTMLButtonElement).disabled).toBe(false));
    vi.spyOn(image, "getBoundingClientRect").mockReturnValue({ left: 0, top: 0, width: 1000, height: 720 } as DOMRect);
    let resolve!: (value: typeof picture) => void;
    vi.mocked(api.redditBrowserInput).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    fireEvent.click(image, { clientX: 100, clientY: 100 });
    fireEvent.click(image, { clientX: 300, clientY: 100 });
    fireEvent.click(image, { clientX: 500, clientY: 100 });
    await waitFor(() => expect(api.redditBrowserInput).toHaveBeenCalledTimes(1));
    await act(async () => { resolve(picture); });
    await waitFor(() => expect(api.redditBrowserInput).toHaveBeenCalledTimes(3));
    expect(vi.mocked(api.redditBrowserInput).mock.calls.map((call) => call[1])).toEqual([
      { kind: "click", x: 100, y: 100 }, { kind: "click", x: 300, y: 100 }, { kind: "click", x: 500, y: 100 },
    ]);
  });
  it("refreshes delayed image changes without dropping clicks or polling after close", async () => {
    const interval = vi.spyOn(window, "setInterval");
    const clearInterval = vi.spyOn(window, "clearInterval");
    const user = userEvent.setup(); const view = render(<RedditBrowserForm />);
    await user.type(await screen.findByLabelText("Reddit login or email"), "alice");
    await user.type(screen.getByLabelText("Reddit password"), "pass");
    await user.click(screen.getByRole("button", { name: "Connect Reddit" }));
    await screen.findByLabelText("Interactive Reddit sign-in");
    await waitFor(() => expect((screen.getByRole("button", { name: "Complete connection" }) as HTMLButtonElement).disabled).toBe(false));
    const timerIndex = interval.mock.calls.findIndex((call) => call[1] === 1000);
    const refresh = interval.mock.calls[timerIndex][0] as () => void;
    const timer = interval.mock.results[timerIndex].value;
    let resolve!: (value: typeof picture) => void;
    vi.mocked(api.redditBrowserInput).mockImplementationOnce(() => new Promise((done) => { resolve = done; }));
    await act(async () => { refresh(); });
    expect(api.redditBrowserInput).toHaveBeenCalledWith("s1", { kind: "refresh" });
    fireEvent.keyDown(screen.getByLabelText("Interactive Reddit sign-in"), { key: "Tab" });
    await act(async () => { refresh(); refresh(); });
    expect(api.redditBrowserInput).toHaveBeenCalledTimes(1);
    await act(async () => { resolve({ ...picture, image: "bmV3" }); });
    expect(api.redditBrowserInput).toHaveBeenLastCalledWith("s1", { kind: "key", key: "Tab" });
    view.unmount();
    expect(clearInterval).toHaveBeenCalledWith(timer);
    await act(async () => { refresh(); });
    expect(api.redditBrowserInput).toHaveBeenCalledTimes(2);
  });
  it("shows a server setup error without accepting fake credentials", async () => {
    vi.mocked(api.redditBrowser).mockResolvedValue({ available: false });
    render(<RedditBrowserForm />);
    expect(await screen.findByText(/needs Chromium installed/)).toBeTruthy();
    expect(screen.queryByLabelText("Reddit password")).toBeNull();
  });
  it("changes only the selected account's proxy and can clear it", async () => {
    vi.spyOn(api, "redditBrowserProxy").mockResolvedValue({ configured: true });
    const done = vi.fn(); const user = userEvent.setup();
    const view=render(<AccountProxyForm account={accountView({ id: 5, browser: { proxy_configured: true } })} onDone={done} />);
    await user.type(screen.getByLabelText("New account proxy"), "http://u:p@proxy:8080");
    await user.click(screen.getByRole("button", { name: "Save proxy" }));
    expect(api.redditBrowserProxy).toHaveBeenCalledWith(5, "http://u:p@proxy:8080");
    view.unmount();
    render(<AccountProxyForm account={accountView({id:5,browser:{proxy_configured:true}})} onDone={done}/>);
    await user.click(screen.getByRole("button", { name: "Use direct connection" }));
    expect(api.redditBrowserProxy).toHaveBeenCalledWith(5, "");
  });
});
