import { useEffect, useState } from "react";
import { t } from "../../i18n";
import type { JobEntry, TaskCommandEntry } from "../../port/port";
import { StudioIcon } from "../StudioIcon";
import { JobName, jobName } from "./JobName";
import { jobElapsed } from "./jobElapsed";

// How an ended job finished, by the kernel's status: only "done" is a success,
// and a job someone stopped is neither a success nor a fault.
const ENDED: Record<string, { label: string; tone: string }> = {
  done: { label: "common.done_2", tone: "var(--ok)" },
  failed: { label: "common.failed", tone: "var(--err)" },
  killed: { label: "plan.stopped", tone: "var(--faint)" },
  interrupted: { label: "common.interrupted", tone: "var(--faint)" },
};

export function Jobs({ jobs, commands, commandsError, onCancel, onView, onStart, onDelete }: {
  jobs: JobEntry[];
  // Registered definitions of this session, which start a job rather than being
  // one: they are listed above the jobs so a running command is both a row that
  // can be started again once it ends and a row that can be stopped now.
  commands?: TaskCommandEntry[];
  commandsError?: string;
  onCancel?: (id: string) => Promise<void>;
  onView?: (id: string) => void;
  onStart?: (id: string) => Promise<void>;
  onDelete?: (id: string) => Promise<void>;
}) {
  const [, tick] = useState(0);
  const [stopping, setStopping] = useState<ReadonlySet<string>>(new Set());
  const [armed, setArmed] = useState("");
  const [pending, setPending] = useState<ReadonlySet<string>>(new Set());

  useEffect(() => {
    if (!armed) return;
    const timer = setTimeout(() => setArmed(""), 3000);
    return () => clearTimeout(timer);
  }, [armed]);

  useEffect(() => {
    if (!jobs.some((j) => j.status === "running")) return;
    const t = setInterval(() => tick((v) => v + 1), 1000);
    return () => clearInterval(t);
  }, [jobs]);

  // "No background jobs" is a sentence about nothing, and it held a block on
  // every rail that was fine. The order it sits in is fixed per posture, so a
  // block coming and going moves only what is below it — cheaper than a
  // standing row a reader has learned to skip.
  const registered = commands ?? [];
  if (jobs.length === 0 && registered.length === 0) return null;
  const live = jobs.filter((j) => j.status === "running");
  const stopAll = () => {
    setArmed("");
    for (const j of live) void onCancel?.(j.id);
  };
  const busy = (id: string) => pending.has(id);
  const run = (id: string, work: (id: string) => Promise<void> | void) => {
    setArmed("");
    setPending((prev) => new Set(prev).add(id));
    void Promise.resolve(work(id)).finally(() => setPending((prev) => {
      const next = new Set(prev);
      next.delete(id);
      return next;
    }));
  };

  return (
    <>
    {registered.length > 0 && (
      <div className="block" data-b="task-commands">
        <div className="lbl">
          {t("common.registered_tasks")}
          <span className="c">{registered.length}</span>
        </div>
        <div className="jobs">
          {registered.map((c) => {
            const running = live.some((j) => j.commandId === c.id);
            const offline = c.available === false;
            return (
              <div className="job" key={c.id} data-status={offline ? "unavailable" : running ? "running" : "registered"}>
                <i className="pip" style={{ background: offline ? "var(--faint)" : running ? "var(--net)" : "var(--dim)" }} />
                <span className="cmd" tabIndex={0} title={offline ? c.unavailableReason ?? c.command : c.command}>{c.name}</span>
                <span className="rt">{offline ? t("common.unavailable") : running ? "running" : ""}</span>
                {onStart && (armed === "cmd:" + c.id ? (
                  <span className="job-confirm">
                    <button type="button" className="job-stop" data-action="task.start" data-target={c.id} disabled={offline || running || busy(c.id)}
                      onClick={() => run(c.id, onStart)}>{t("common.start")}</button>
                  </span>
                ) : (
                  <button type="button" className="job-run" data-action="task.arm-start" data-target={c.id}
                    disabled={offline || running || busy(c.id)} title={t("common.start")} aria-label={t("common.start")}
                    onClick={() => run(c.id, onStart)}>
                    <StudioIcon name="play" />
                  </button>
                ))}
                {onDelete && (armed === "del:" + c.id ? (
                  <span className="job-confirm">
                    <span>{t("common.confirm_delete")}</span>
                    <button type="button" className="job-stop" data-action="task.delete" data-target={c.id} disabled={busy(c.id)}
                      onClick={() => run(c.id, onDelete)}>{t("panels.ok")}</button>
                  </span>
                ) : (
                  <button type="button" className="job-stop" data-action="task.arm-delete" data-target={c.id} disabled={busy(c.id)}
                    title={t("common.delete")} aria-label={t("common.delete")} onClick={() => setArmed("del:" + c.id)}>
                    <StudioIcon name="trash" />
                  </button>
                ))}
              </div>
            );
          })}
        </div>
        {commandsError ? <div className="job-confirm"><span>{t("common.unavailable")}</span></div> : null}
      </div>
    )}
    {jobs.length > 0 && (
    <div className="block" data-b="jobs">
      <div className="lbl">
        {t("common.background_jobs")}
        {onCancel && live.length >= 2 && (armed === "*" ? (
          <span className="job-confirm">
            <span>{t("panels.stop_all")}</span>
            <button type="button" className="job-stop" data-action="job.cancel-all" onClick={stopAll}>{t("panels.ok")}</button>
          </span>
        ) : (
          <button type="button" className="job-stop-all" data-action="job.arm-all" onClick={() => setArmed("*")}>{t("common.stop_all")}</button>
        ))}
        <span className="c">{jobs.length}</span>
      </div>
      <div className="jobs">
        {jobs.map((j) => {
          const running = j.status === "running";
          const ended = ENDED[j.status];
          return (
            <div className="job" key={j.id} data-done={running ? undefined : ""} data-status={j.status}>
              <i
                className="pip"
                style={running ? { background: "var(--net)", animation: "tick 1.6s ease-in-out infinite" } : { background: ended?.tone ?? "var(--faint)" }}
              />
              <JobName job={j}>
                {running && onView ? (
                  <button type="button" className="cmd job-view" data-action="job.view" data-target={j.id} aria-label={t("panels.view_output_cmd", { cmd: jobName(j) })} onClick={() => onView(j.id)}>
                    {jobName(j)}
                  </button>
                ) : (
                  <span className="cmd" tabIndex={0}>{jobName(j)}</span>
                )}
              </JobName>
              <span className="rt">{running ? jobElapsed(Date.now() - j.startedAt) : ended ? t(ended.label) : j.status}</span>
              {running && onCancel && (armed === j.id ? (
                <span className="job-confirm">
                  <span>{t("panels.stop")}</span>
                  <button
                    type="button"
                    className="job-stop"
                    data-action="job.cancel"
                    data-target={j.id}
                    disabled={stopping.has(j.id)}
                    aria-label={t("panels.confirm_stopping_background_task_cmd", { cmd: jobName(j) })}
                    onClick={() => {
                      setArmed("");
                      setStopping((prev) => new Set(prev).add(j.id));
                      void onCancel(j.id).finally(() => setStopping((prev) => {
                        const next = new Set(prev);
                        next.delete(j.id);
                        return next;
                      }));
                    }}
                  >
                    {t("panels.ok")}
                  </button>
                </span>
              ) : (
                <button
                  type="button"
                  className="job-stop"
                  data-action="job.arm"
                  data-target={j.id}
                  disabled={stopping.has(j.id)}
                  title={t("common.stop")}
                  aria-label={t("panels.stop_background_job_cmd", { cmd: jobName(j) })}
                  onClick={() => setArmed(j.id)}
                >
                  <StudioIcon name="stop" />
                </button>
              ))}
            </div>
          );
        })}
      </div>
    </div>
    )}
    </>
  );
}
