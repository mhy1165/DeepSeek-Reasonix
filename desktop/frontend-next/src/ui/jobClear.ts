// Cleared ended jobs are this window's memory, not the kernel's: /status keeps
// returning them until the session is purged, so the set has to outlive the
// window or every restart would bring back rows the person already dismissed.
const PREFIX = "reasonix.jobs.cleared.";

const keyOf = (session: string) => PREFIX + session;

export function readClearedJobs(session: string): ReadonlySet<string> {
  try {
    const raw = localStorage.getItem(keyOf(session));
    const ids = raw ? (JSON.parse(raw) as unknown) : null;
    return new Set(Array.isArray(ids) ? ids.filter((id): id is string => typeof id === "string") : []);
  } catch {
    return new Set();
  }
}

export function writeClearedJobs(session: string, ids: ReadonlySet<string>): void {
  try {
    localStorage.setItem(keyOf(session), JSON.stringify([...ids]));
  } catch {
    // A window with no storage keeps the rows cleared for as long as it lives.
  }
}
