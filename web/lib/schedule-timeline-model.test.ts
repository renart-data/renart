import { describe, expect, it } from "vitest";

import {
  scheduleExpectedSlots,
  scheduleTimelineAxis,
  scheduleTimelineLeft,
  scheduleTimelineWindow,
  type ScheduleTimelineWindow,
} from "./schedule-timeline-model";

const now = Date.parse("2026-09-02T12:00:00.000Z");

describe("schedule timeline model", () => {
  it("builds a stable time window and axis around now", () => {
    const window = scheduleTimelineWindow("6hr", "regular", now);
    expect(window).toEqual({
      start: Date.parse("2026-09-02T10:00:00.000Z"),
      end: Date.parse("2026-09-02T18:00:00.000Z"),
      bucket: "6hr",
      density: "regular",
    });
    const axis = scheduleTimelineAxis(window, "en-US");
    expect(axis).toHaveLength(9);
    expect(axis[0]?.left).toBe(0);
    expect(axis.at(-1)?.left).toBe(100);
  });

  it("keeps the persisted next occurrence and de-duplicates its projection", () => {
    const window: ScheduleTimelineWindow = {
      start: Date.parse("2026-09-02T10:00:00.000Z"),
      end: Date.parse("2026-09-02T14:00:00.000Z"),
      bucket: "6hr",
      density: "regular",
    };
    const slots = scheduleExpectedSlots(
      {
        schedule: "0 * * * *",
        timezone: "UTC",
        enabled: true,
        next_run_at: "2026-09-02T13:00:00.000Z",
      },
      window,
      now,
    );
    expect(slots.filter((slot) => slot.time === Date.parse("2026-09-02T13:00:00.000Z"))).toEqual([
      expect.objectContaining({ kind: "persisted", phase: "future" }),
    ]);
    expect(slots.map((slot) => slot.time)).toEqual([
      Date.parse("2026-09-02T13:00:00.000Z"),
      Date.parse("2026-09-02T10:00:00.000Z"),
      Date.parse("2026-09-02T11:00:00.000Z"),
      Date.parse("2026-09-02T12:00:00.000Z"),
      Date.parse("2026-09-02T14:00:00.000Z"),
    ]);
  });

  it("retains a persisted occurrence when a cron expression cannot be projected", () => {
    const window = scheduleTimelineWindow("1hr", "compact", now);
    const slots = scheduleExpectedSlots(
      {
        schedule: "unsupported expression",
        timezone: "UTC",
        enabled: true,
        next_run_at: new Date(now + 15 * 60 * 1000).toISOString(),
      },
      window,
      now,
    );
    expect(slots).toEqual([expect.objectContaining({ kind: "persisted" })]);
  });

  it("bounds positions to the current window", () => {
    const window = scheduleTimelineWindow("1hr", "regular", now);
    expect(scheduleTimelineLeft(window.start, window)).toBe(0);
    expect(scheduleTimelineLeft(window.end, window)).toBe(100);
    expect(scheduleTimelineLeft(window.start - 1, window)).toBeNull();
    expect(scheduleTimelineLeft(Number.NaN, window)).toBeNull();
  });
});
