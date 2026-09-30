import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { RedditForm } from "../components/ConnectForms";

describe("RedditForm", () => {
  it("reuses a configured app and gives a link for a private window", async () => {
    vi.spyOn(api, "redditApp").mockResolvedValue({ configured: true });
    vi.spyOn(api, "redditAuthorize").mockResolvedValue({ authorize_url: "https://www.reddit.com/api/v1/authorize?state=x", redirect_uri: "http://localhost/oauth/reddit/callback" });
    const user = userEvent.setup();
    render(<RedditForm projectId={3} />);
    await user.click(await screen.findByRole("button", { name: "Add another Reddit account" }));
    expect(api.redditAuthorize).toHaveBeenCalledWith("", "", 3);
    expect(await screen.findByLabelText("Reddit authorization link")).toBeTruthy();
    expect(screen.getAllByText(/private window/)).toHaveLength(2);
    expect(screen.queryByLabelText("Client ID")).toBeNull();
  });

  it("asks for app credentials on first connection", async () => {
    vi.spyOn(api, "redditApp").mockResolvedValue({ configured: false });
    render(<RedditForm />);
    expect(await screen.findByLabelText("Client ID")).toBeTruthy();
    expect(screen.getByRole("button", { name: "Continue to Reddit" })).toBeTruthy();
  });
});
