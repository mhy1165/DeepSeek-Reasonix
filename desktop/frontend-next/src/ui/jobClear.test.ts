// @vitest-environment jsdom
import { beforeEach, expect, it } from "vitest";
import { readClearedJobs, writeClearedJobs } from "./jobClear";

beforeEach(() => localStorage.clear());

it("remembers cleared jobs for the session that cleared them", () => {
  writeClearedJobs("/sessions/a.jsonl", new Set(["bash-1", "bash-2"]));
  expect([...readClearedJobs("/sessions/a.jsonl")].sort()).toEqual(["bash-1", "bash-2"]);
});

it("keeps one session's cleared rows out of another's", () => {
  writeClearedJobs("/sessions/a.jsonl", new Set(["bash-1"]));
  expect(readClearedJobs("/sessions/b.jsonl").size).toBe(0);
});

it("answers an empty set for a session that never cleared anything", () => {
  expect(readClearedJobs("/sessions/new.jsonl").size).toBe(0);
});

it("survives a value that is not the set it wrote", () => {
  localStorage.setItem("reasonix.jobs.cleared./sessions/a.jsonl", "{not json");
  expect(readClearedJobs("/sessions/a.jsonl").size).toBe(0);
});
