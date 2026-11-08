// A background job's lifecycle is /status changing: the job list, its count and
// the registered-task rows all come from that snapshot. A live turn is not a
// precondition — a registered task runs outside any turn, and one started from a
// paired device produces no session event this pane would otherwise react to.
const JOB_NOTICES: ReadonlySet<string> = new Set([
  "job_started",
  "job_finished",
  "job_killed",
  "job_failed",
  "job_stalled",
]);

// jobNoticeMovesStatus says whether a notice means the background-job snapshot
// has to be read again. What the notice itself carries is a line in the
// transcript; what it announces is elsewhere, in /status.
export function jobNoticeMovesStatus(code: string | undefined): boolean {
  return !!code && JOB_NOTICES.has(code);
}
