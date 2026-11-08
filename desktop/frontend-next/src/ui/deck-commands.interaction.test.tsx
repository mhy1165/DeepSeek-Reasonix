// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "./testkit";
import { DeckChips, type Deck } from "./DeckChips";
import { useState } from "react";
import type { JobEntry, TaskCommandEntry } from "../port/port";

afterEach(cleanup);

const COMMAND: TaskCommandEntry = {
  id: "ruoyi-auth", name: "auth", runner: "bash", cwd: "backend/RuoYi-Cloud",
  command: "java -jar ruoyi-auth.jar", shell: { kind: "pwsh", path: "pwsh", args: [] },
  createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z", available: true,
};

const JOB: JobEntry = {
  id: "bash-31", kind: "bash", label: "auth", status: "running", startedAt: Date.now(),
  restartable: true,
  recipe: { runner: "bash", cwd: "backend/RuoYi-Cloud", command: "java -jar ruoyi-auth.jar", shell: { kind: "pwsh", path: "pwsh", args: [] } },
};

function Host(props: { jobs: JobEntry[]; commands?: TaskCommandEntry[]; onStart?: (id: string) => Promise<void>; onRestart?: (id: string) => Promise<void>; onRegister?: (id: string, name: string) => Promise<void> }) {
  const [open, setOpen] = useState<Deck>("jobs");
  return <DeckChips tasks={[]} jobs={props.jobs} commands={props.commands} open={open} onOpen={setOpen} onStartCommand={props.onStart} onRestartJob={props.onRestart} onRegisterJob={props.onRegister} onCancelJob={async () => {}} />;
}

// The deck is the surface a person actually has open while work runs, so the
// registered rows and the replay/register controls belong here too.
it("lists registered commands in the deck and starts one", async () => {
  const onStart = vi.fn().mockResolvedValue(undefined);
  render(<Host jobs={[]} commands={[COMMAND]} onStart={onStart} />);
  expect(screen.getByText("已注册任务")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "开始" }));
  expect(onStart).toHaveBeenCalledWith("ruoyi-auth");
});

it("restarts a running job that kept a recipe", async () => {
  const onRestart = vi.fn().mockResolvedValue(undefined);
  render(<Host jobs={[JOB]} onRestart={onRestart} />);
  await userEvent.click(screen.getByRole("button", { name: "重启" }));
  expect(onRestart).toHaveBeenCalledWith("bash-31");
});

it("registers a running job after its name is confirmed in the deck", async () => {
  const onRegister = vi.fn().mockResolvedValue(undefined);
  render(<Host jobs={[JOB]} onRegister={onRegister} />);
  await userEvent.click(screen.getByRole("button", { name: "注册" }));
  const name = screen.getByRole("textbox", { name: "注册" }) as HTMLInputElement;
  expect(name.value).toBe("auth");
  await userEvent.clear(name);
  await userEvent.type(name, "auth 服务");
  await userEvent.click(screen.getByRole("button", { name: "确定" }));
  expect(onRegister).toHaveBeenCalledWith("bash-31", "auth 服务");
});
