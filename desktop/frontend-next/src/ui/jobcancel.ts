import type { AgentPort } from "../port/port";

// Whatever the kernel answers, the list is read again afterwards: a refusal
// means the list the person was looking at no longer matches the kernel's.
export function cancelJob(port: Pick<AgentPort, "cancelJob">, id: string, notify: (e: unknown) => void, refresh: () => Promise<unknown> | void): Promise<unknown> {
  return port.cancelJob(id).catch(notify).then(refresh);
}

// A registered command starts a job rather than being one, and the list has to
// be read again either way: a refusal means the kernel's list is not the one
// the person was looking at.
export function startTaskCommand(port: Pick<AgentPort, "startTaskCommand">, id: string, notify: (e: unknown) => void, refresh: () => Promise<unknown> | void): Promise<unknown> {
  return port.startTaskCommand(id).catch(notify).then(refresh);
}

export function deleteTaskCommand(port: Pick<AgentPort, "deleteTaskCommand">, id: string, notify: (e: unknown) => void, refresh: () => Promise<unknown> | void): Promise<unknown> {
  return port.deleteTaskCommand(id).catch(notify).then(refresh);
}

// A restart hands the job over in place when it kept a recipe and starts a new
// attempt from the definition when it did not; either way the list is stale.
export function restartJob(port: Pick<AgentPort, "restartJob">, id: string, notify: (e: unknown) => void, refresh: () => Promise<unknown> | void): Promise<unknown> {
  return port.restartJob(id).catch(notify).then(refresh);
}

// Registering changes the command list, not the job that supplied it.
export function registerTaskCommand(port: Pick<AgentPort, "registerTaskCommand">, jobId: string, name: string, notify: (e: unknown) => void, refresh: () => Promise<unknown> | void): Promise<unknown> {
  return port.registerTaskCommand(jobId, name).catch(notify).then(refresh);
}
