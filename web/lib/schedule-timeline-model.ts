export const scheduleTimelineBuckets = ["1hr", "6hr", "12hr", "24hr"] as const;

export type ScheduleTimelineBucket = (typeof scheduleTimelineBuckets)[number];
export type ScheduleTimelineDensity = "compact" | "regular";

export type ScheduleTimelineWindow = {
  start: number;
  end: number;
  bucket: ScheduleTimelineBucket;
  density: ScheduleTimelineDensity;
};

export type ScheduleTimelineTick = {
  key: string;
  label: string;
  left: number;
  at: number;
};

export type ScheduleTimelineInput = {
  schedule: string;
  timezone?: string;
  enabled: boolean;
  next_run_at?: string;
};

export type ScheduleTimelineSlot = {
  at: string;
  time: number;
  left: number;
  width: number;
  kind: "persisted" | "projected";
  phase: "past" | "future";
};

const minute = 60 * 1000;
const hour = 60 * minute;

export function scheduleTimelineWindow(
  bucket: ScheduleTimelineBucket,
  density: ScheduleTimelineDensity,
  now = Date.now(),
): ScheduleTimelineWindow {
  const stepMs = scheduleTimelineTickStepMs(bucket, density);
  const bucketMs = scheduleTimelineBucketHours(bucket) * hour;
  const start = floorTimelineTime(now - bucketMs / 4, stepMs);
  const end = floorTimelineTime(now, stepMs) + bucketMs;
  return { start, end, bucket, density };
}

export function scheduleTimelineAxis(
  window: ScheduleTimelineWindow,
  locale?: Intl.LocalesArgument,
): ScheduleTimelineTick[] {
  const stepMs = scheduleTimelineTickStepMs(window.bucket, window.density);
  const formatter = new Intl.DateTimeFormat(locale, { hour: "numeric", minute: "2-digit" });
  const ticks: ScheduleTimelineTick[] = [];
  for (let time = window.start; time <= window.end + 1; time += stepMs) {
    ticks.push({
      key: `${window.bucket}-${time}`,
      label: formatter.format(new Date(time)),
      left: ((time - window.start) / Math.max(window.end - window.start, 1)) * 100,
      at: time,
    });
  }
  return ticks;
}

export function scheduleExpectedSlots(
  schedule: ScheduleTimelineInput,
  window: ScheduleTimelineWindow,
  now = Date.now(),
): ScheduleTimelineSlot[] {
  const persistedNext = schedule.next_run_at ? Date.parse(schedule.next_run_at) : Number.NaN;
  const parsed = parseStandardCron(normalizeSchedule(schedule.schedule));
  const slots: ScheduleTimelineSlot[] = [];
  const addSlot = (time: number, kind: ScheduleTimelineSlot["kind"]) => {
    const left = scheduleTimelineLeft(time, window);
    if (left === null) return;
    slots.push({
      at: new Date(time).toISOString(),
      time,
      left,
      width: window.bucket === "1hr" ? 2.5 : 1.4,
      kind,
      phase: time < now ? "past" : "future",
    });
  };

  if (Number.isFinite(persistedNext)) {
    addSlot(persistedNext, "persisted");
  }
  if (!parsed) {
    return slots;
  }
  for (let time = floorTimelineTime(window.start, minute); time <= window.end; time += minute) {
    if (!cronMatches(parsed, time, schedule.timezone)) continue;
    if (Number.isFinite(persistedNext) && Math.abs(time - persistedNext) < minute) continue;
    addSlot(time, "projected");
  }
  return slots;
}

export function scheduleTimelineLeft(time: number, window: ScheduleTimelineWindow) {
  if (!Number.isFinite(time) || time < window.start || time > window.end) return null;
  return ((time - window.start) / Math.max(window.end - window.start, 1)) * 100;
}

export function scheduleTimelineBucketHours(bucket: ScheduleTimelineBucket) {
  return {
    "1hr": 1,
    "6hr": 6,
    "12hr": 12,
    "24hr": 24,
  }[bucket];
}

function scheduleTimelineTickStepMs(
  bucket: ScheduleTimelineBucket,
  density: ScheduleTimelineDensity,
) {
  if (density === "compact") {
    return {
      "1hr": 30 * minute,
      "6hr": 2 * hour,
      "12hr": 4 * hour,
      "24hr": 6 * hour,
    }[bucket];
  }
  return {
    "1hr": 15 * minute,
    "6hr": hour,
    "12hr": 2 * hour,
    "24hr": 4 * hour,
  }[bucket];
}

function floorTimelineTime(value: number, stepMs: number) {
  return Math.floor(value / stepMs) * stepMs;
}

type CronField = {
  values: Set<number>;
  wildcard: boolean;
};

type ParsedCron = {
  minute: CronField;
  hour: CronField;
  dayOfMonth: CronField;
  month: CronField;
  dayOfWeek: CronField;
};

function normalizeSchedule(schedule: string) {
  const normalized = schedule.trim().toLowerCase();
  if (
    !normalized ||
    normalized === "daily" ||
    normalized === "@daily" ||
    normalized === "@midnight"
  ) {
    return "0 0 * * *";
  }
  if (normalized === "hourly" || normalized === "@hourly") return "0 * * * *";
  if (normalized === "weekly" || normalized === "@weekly") return "0 0 * * 0";
  if (normalized === "monthly" || normalized === "@monthly") return "0 0 1 * *";
  if (
    normalized === "yearly" ||
    normalized === "annually" ||
    normalized === "@yearly" ||
    normalized === "@annually"
  ) {
    return "0 0 1 1 *";
  }
  return normalized;
}

function parseStandardCron(schedule: string): ParsedCron | null {
  const fields = schedule.trim().split(/\s+/);
  if (fields.length !== 5) return null;
  const [minuteValue, hourValue, dayOfMonthValue, monthValue, dayOfWeekValue] = fields;
  const minuteField = parseCronField(minuteValue, 0, 59);
  const hourField = parseCronField(hourValue, 0, 23);
  const dayOfMonthField = parseCronField(dayOfMonthValue, 1, 31);
  const monthField = parseCronField(monthValue, 1, 12, monthNames);
  const dayOfWeekField = parseCronField(dayOfWeekValue, 0, 7, dayNames);
  if (!minuteField || !hourField || !dayOfMonthField || !monthField || !dayOfWeekField) {
    return null;
  }
  return {
    minute: minuteField,
    hour: hourField,
    dayOfMonth: dayOfMonthField,
    month: monthField,
    dayOfWeek: dayOfWeekField,
  };
}

const monthNames: Record<string, number> = {
  jan: 1,
  feb: 2,
  mar: 3,
  apr: 4,
  may: 5,
  jun: 6,
  jul: 7,
  aug: 8,
  sep: 9,
  oct: 10,
  nov: 11,
  dec: 12,
};

const dayNames: Record<string, number> = {
  sun: 0,
  mon: 1,
  tue: 2,
  wed: 3,
  thu: 4,
  fri: 5,
  sat: 6,
};

function parseCronField(
  value: string,
  min: number,
  max: number,
  aliases: Record<string, number> = {},
) {
  const values = new Set<number>();
  let wildcard = false;
  for (const rawPart of value.split(",")) {
    const [rangePartRaw, stepPart] = rawPart.split("/");
    const rangePart = rangePartRaw.trim().toLowerCase();
    const step = stepPart ? Number(stepPart) : 1;
    if (!Number.isInteger(step) || step <= 0) return null;
    const rangeValues = cronRange(rangePart, min, max, aliases);
    if (!rangeValues) return null;
    wildcard ||= rangeValues.wildcard;
    for (let current = rangeValues.start; current <= rangeValues.end; current += step) {
      values.add(current);
      if (max === 7 && current === 7) values.add(0);
    }
  }
  return { values, wildcard };
}

function cronRange(value: string, min: number, max: number, aliases: Record<string, number>) {
  if (value === "*" || value === "?") {
    return { start: min, end: max, wildcard: true };
  }
  const [startRaw, endRaw] = value.split("-");
  const start = cronNumber(startRaw, aliases);
  const end = cronNumber(endRaw ?? startRaw, aliases);
  if (
    !Number.isInteger(start) ||
    !Number.isInteger(end) ||
    start < min ||
    end > max ||
    start > end
  ) {
    return null;
  }
  return { start, end, wildcard: false };
}

function cronNumber(value: string, aliases: Record<string, number>) {
  const normalized = value.trim().toLowerCase();
  return aliases[normalized] ?? Number(normalized);
}

function cronMatches(parsed: ParsedCron, time: number, timezone: string | undefined) {
  const parts = zonedDateParts(new Date(time), timezone);
  if (!parts) return false;
  const dayOfWeekMatches =
    parsed.dayOfWeek.values.has(parts.dayOfWeek) ||
    (parts.dayOfWeek === 0 && parsed.dayOfWeek.values.has(7));
  const dayMatches =
    parsed.dayOfMonth.wildcard || parsed.dayOfWeek.wildcard
      ? parsed.dayOfMonth.values.has(parts.dayOfMonth) && dayOfWeekMatches
      : parsed.dayOfMonth.values.has(parts.dayOfMonth) || dayOfWeekMatches;
  return (
    parsed.minute.values.has(parts.minute) &&
    parsed.hour.values.has(parts.hour) &&
    dayMatches &&
    parsed.month.values.has(parts.month)
  );
}

function zonedDateParts(date: Date, timezone: string | undefined) {
  try {
    const formatter = new Intl.DateTimeFormat("en-US", {
      timeZone: timezone || "UTC",
      year: "numeric",
      month: "numeric",
      day: "numeric",
      hour: "numeric",
      minute: "numeric",
      hour12: false,
    });
    const values = Object.fromEntries(
      formatter.formatToParts(date).map((part) => [part.type, part.value]),
    );
    const year = Number(values.year);
    const month = Number(values.month);
    const dayOfMonth = Number(values.day);
    const hour = Number(values.hour) % 24;
    const minute = Number(values.minute);
    if (![year, month, dayOfMonth, hour, minute].every(Number.isFinite)) return null;
    return {
      month,
      dayOfMonth,
      dayOfWeek: new Date(Date.UTC(year, month - 1, dayOfMonth)).getUTCDay(),
      hour,
      minute,
    };
  } catch {
    return null;
  }
}
