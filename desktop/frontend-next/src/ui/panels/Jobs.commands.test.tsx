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

// One command, one running job: while its job runs the definition moves down
// into the jobs list, so nothing up here offers a second start.
it("moves a running command out of the registered list", () => {
  const running: JobEntry = {
    id: "bash-12", kind: "bash", label: "auth", status: "running",
    startedAt: Date.now(), commandId: "ruoyi-auth", restartable: true,
  };
  render(<Jobs jobs={[running]} commands={[COMMAND]} onStart={vi.fn()} onDelete={vi.fn()} />);
  expect(screen.queryByRole("button", { name: "开始" })).toBeNull();
  expect(screen.queryByRole("button", { name: "删除" })).toBeNull();
});

// A command whose shell or working directory is gone says so, and cannot be
// started from the list at all.
it("marks an unavailable command and keeps it from starting", () => {
  render(<Jobs jobs={[]} commands={[{ ...COMMAND, available: false, unavailableReason: "the shell is gone" }]} onStart={vi.fn()} onDelete={vi.fn()} />);
  expect(screen.getByText("不可用")).toBeTruthy();
  expect((screen.getByRole("button", { name: "开始" }) as HTMLButtonElement).disabled).toBe(true);
});

// A running job that kept a recipe can be replayed in place; one that did not
// offers no restart at all.
it("offers restart only for a running job that kept a recipe, and calls it by job id", async () => {
  const onRestart = vi.fn().mockResolvedValue(undefined);
  const kept: JobEntry = { id: "bash-12", kind: "bash", label: "auth", status: "running", startedAt: Date.now(), restartable: true };
  const bare: JobEntry = { id: "bash-13", kind: "bash", label: "build", status: "running", startedAt: Date.now() };
  render(<Jobs jobs={[kept, bare]} commands={[]} onRestart={onRestart} onCancel={vi.fn()} />);
  expect(screen.getAllByRole("button", { name: "重启" })).toHaveLength(1);
  await userEvent.click(screen.getByRole("button", { name: "重启" }));
  expect(onRestart).toHaveBeenCalledWith("bash-12");
});

// Registering asks once, naming the command and the folder it would run in,
// before the definition is written.
it("registers a running job only after the confirmation shows what will be saved", async () => {
  const onRegister = vi.fn().mockResolvedValue(undefined);
  const job: JobEntry = {
    id: "bash-20", kind: "bash", label: "auth", status: "running", startedAt: Date.now(),
    recipe: { runner: "bash", cwd: "backend/RuoYi-Cloud", command: "java -jar ruoyi-auth.jar", shell: { kind: "pwsh", path: "pwsh", args: [] } },
  };
  render(<Jobs jobs={[job]} commands={[]} onRegister={onRegister} onCancel={vi.fn()} />);
  await userEvent.click(screen.getByRole("button", { name: "注册" }));
  expect(onRegister).not.toHaveBeenCalled();
  const name = screen.getByRole("textbox", { name: "注册" }) as HTMLInputElement;
  expect(name.value).toBe("auth");
  expect(screen.getByText("backend/RuoYi-Cloud")).toBeTruthy();
  await userEvent.clear(name);
  await userEvent.type(name, "构建 auth");
  await userEvent.click(screen.getByRole("button", { name: "确定" }));
  expect(onRegister).toHaveBeenCalledWith("bash-20", "构建 auth");
});
