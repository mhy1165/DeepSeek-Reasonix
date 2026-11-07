// @vitest-environment jsdom
import { afterEach, expect, it, vi } from "vitest";
import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import "../testkit";
import { Jobs } from "./Jobs";
import type { JobEntry, TaskCommandEntry } from "../../port/port";

afterEach(cleanup);

const COMMAND: TaskCommandEntry = {
  id: "ruoyi-auth",
  name: "auth",
  runner: "bash",
  cwd: "backend/RuoYi-Cloud",
  command: "java -jar ruoyi-auth/target/ruoyi-auth.jar",
  shell: { kind: "pwsh", path: "pwsh", args: [] },
  createdAt: "2026-11-07T10:00:00Z",
  updatedAt: "2026-11-07T10:00:00Z",
  available: true,
};

// A registered command is a definition, so it is drawn even with no job
// running: that is the row a person starts from.
it("draws a registered command with no job running, and starts it by its own id", async () => {
  const onStart = vi.fn().mockResolvedValue(undefined);
  render(<Jobs jobs={[]} commands={[COMMAND]} onStart={onStart} onDelete={vi.fn()} />);
  expect(screen.getByText("已注册任务")).toBeTruthy();
  expect(screen.getByText("auth")).toBeTruthy();
  await userEvent.click(screen.getByRole("button", { name: "开始" }));
  expect(onStart).toHaveBeenCalledWith("ruoyi-auth");
});

// Deleting asks once, and the confirm names what is being deleted rather than
// repeating the row's own button.
it("deletes only after the confirmation is accepted", async () => {
  const onDelete = vi.fn().mockResolvedValue(undefined);
  render(<Jobs jobs={[]} commands={[COMMAND]} onStart={vi.fn()} onDelete={onDelete} />);
  await userEvent.click(screen.getByRole("button", { name: "删除" }));
  expect(onDelete).not.toHaveBeenCalled();
  await userEvent.click(screen.getByRole("button", { name: "确定" }));
  expect(onDelete).toHaveBeenCalledWith("ruoyi-auth");
});

// One command, one running job: a row whose job is already running cannot be
// started a second time.
it("refuses a second start while the command is running", () => {
  const running: JobEntry = {
    id: "bash-12", kind: "bash", label: "auth", status: "running",
    startedAt: Date.now(), commandId: "ruoyi-auth", restartable: true,
  };
  render(<Jobs jobs={[running]} commands={[COMMAND]} onStart={vi.fn()} onDelete={vi.fn()} />);
  const start = screen.getByRole("button", { name: "开始" }) as HTMLButtonElement;
  expect(start.disabled).toBe(true);
});

// A command whose shell or working directory is gone says so, and cannot be
// started from the list at all.
it("marks an unavailable command and keeps it from starting", () => {
  render(<Jobs jobs={[]} commands={[{ ...COMMAND, available: false, unavailableReason: "the shell is gone" }]} onStart={vi.fn()} onDelete={vi.fn()} />);
  expect(screen.getByText("不可用")).toBeTruthy();
  expect((screen.getByRole("button", { name: "开始" }) as HTMLButtonElement).disabled).toBe(true);
});
