import { expect, it } from "vitest";
import { jobNoticeMovesStatus } from "./jobSync";

// The five notices a job writes are the only ones whose subject is the job list
// in /status; everything else about them is already a transcript line.
it("asks for the job snapshot on the notices a job writes", () => {
  for (const code of ["job_started", "job_finished", "job_killed", "job_failed", "job_stalled"]) {
    expect(jobNoticeMovesStatus(code)).toBe(true);
  }
});

it("leaves other notices and a missing code alone", () => {
  expect(jobNoticeMovesStatus("display_currency")).toBe(false);
  expect(jobNoticeMovesStatus(undefined)).toBe(false);
});
