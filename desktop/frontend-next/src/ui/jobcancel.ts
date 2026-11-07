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
