import { describe, it, expect } from 'vitest';
import { formatEpochUTC, formatDuration, showIfNonZero } from '../lib/format';

describe('formatEpochUTC', () => {
  // Rendering shape used under the task_id cell in the task list.
  // Compact `YYYY-MM-DD HH:mmZ` — no seconds, no locale drift, one
  // line so it fits under the id in the italic secondary style.
  it('renders epoch seconds as UTC "YYYY-MM-DD HH:mmZ"', () => {
    // 2026-07-02 08:11:00 UTC — matches the alpha2 checkm2 crisis
    // timestamps the caller wants to display.
    const epoch = Date.UTC(2026, 6, 2, 8, 11, 0) / 1000;
    expect(formatEpochUTC(epoch)).toBe('2026-07-02 08:11Z');
  });

  it('pads single-digit month/day/hour/minute', () => {
    // 2025-03-05 09:07:00 UTC — every field is single-digit and must
    // be zero-padded to two chars. Regression: if the formatter uses
    // toISOString().slice(...) it gets this right; a hand-rolled
    // format without padding would produce "2025-3-5 9:7Z".
    const epoch = Date.UTC(2025, 2, 5, 9, 7, 0) / 1000;
    expect(formatEpochUTC(epoch)).toBe('2025-03-05 09:07Z');
  });

  it('accepts epoch as a string (protobuf-ts int64 long_type_string)', () => {
    // The UI's Task type has `createdAt?: string` because the proto
    // stub emits int64 as string. formatEpochUTC must accept both.
    expect(formatEpochUTC('1751443860')).toBe(formatEpochUTC(1751443860));
  });

  it('returns empty for null / undefined / 0 / negative / non-finite', () => {
    // Task rows for statuses that were never assigned a created_at
    // (shouldn't happen after 2026-07-02 but defensive against the
    // pre-proto-bump task rows in extended workflows) get an empty
    // string so the caller can drop the row without a conditional.
    expect(formatEpochUTC(null)).toBe('');
    expect(formatEpochUTC(undefined)).toBe('');
    expect(formatEpochUTC(0)).toBe('');
    expect(formatEpochUTC(-1)).toBe('');
    expect(formatEpochUTC('0')).toBe('');
    expect(formatEpochUTC('')).toBe('');
    expect(formatEpochUTC(NaN)).toBe('');
    expect(formatEpochUTC(Infinity)).toBe('');
    expect(formatEpochUTC('not a number')).toBe('');
  });

  it('does not mutate output based on local timezone', () => {
    // Same epoch in a system whose Date locale differs from UTC must
    // still render UTC. If the formatter accidentally used
    // getHours() instead of getUTCHours(), this test would fail on
    // any CI runner that isn't in UTC (which is most of them).
    const epoch = Date.UTC(2026, 0, 1, 0, 0, 0) / 1000;
    expect(formatEpochUTC(epoch)).toBe('2026-01-01 00:00Z');
  });
});

describe('formatDuration (regression guard alongside the new helper)', () => {
  it('formats seconds into compact h/m/s parts', () => {
    expect(formatDuration(0)).toBe('0s');
    expect(formatDuration(1)).toBe('1s');
    expect(formatDuration(59)).toBe('59s');
    expect(formatDuration(60)).toBe('1m');
    expect(formatDuration(61)).toBe('1m1s');
    expect(formatDuration(3661)).toBe('1h1m1s');
  });

  it('returns 0s for null/zero/negative', () => {
    expect(formatDuration(0)).toBe('0s');
    expect(formatDuration(-5)).toBe('0s');
    // @ts-expect-error — defensive on runtime null despite the signature
    expect(formatDuration(null)).toBe('0s');
  });
});

describe('showIfNonZero (regression guard)', () => {
  it('blanks zero and undefined, prints the rest', () => {
    expect(showIfNonZero(0)).toBe('');
    expect(showIfNonZero(undefined)).toBe('');
    expect(showIfNonZero(1)).toBe('1');
    expect(showIfNonZero(237)).toBe('237');
  });
});
