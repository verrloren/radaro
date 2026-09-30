import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { ProxySettingsForm } from "../components/ProxySettingsForm";

describe("ProxySettingsForm", () => {
  it("saves a replacement without reading back credentials", async () => {
    vi.spyOn(api, "proxySettings").mockResolvedValue({ configured: true, origin: "env" });
    vi.spyOn(api, "saveProxy").mockResolvedValue({ configured: true, origin: "ui" });
    const onChanged = vi.fn();
    const user = userEvent.setup();
    render(<ProxySettingsForm onChanged={onChanged} />);
    expect(await screen.findByText("from environment")).toBeTruthy();
    const field = screen.getByLabelText("Proxy URL") as HTMLInputElement;
    expect(field.value).toBe("");
    await user.type(field, "http://proxy.example:8080");
    await user.click(screen.getByRole("button", { name: "Save proxy" }));
    expect(api.saveProxy).toHaveBeenCalledWith("http://proxy.example:8080");
    expect(onChanged).toHaveBeenCalled();
    expect(field.value).toBe("");
  });

  it("removes only a saved override", async () => {
    vi.spyOn(api, "proxySettings").mockResolvedValue({ configured: true, origin: "ui" });
    vi.spyOn(api, "resetProxy").mockResolvedValue({ configured: false, origin: "" });
    const user = userEvent.setup();
    render(<ProxySettingsForm onChanged={vi.fn()} />);
    await user.click(await screen.findByRole("button", { name: "Remove saved proxy" }));
    expect(api.resetProxy).toHaveBeenCalled();
  });
});
