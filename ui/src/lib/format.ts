// src/lib/format.ts
export function formatDuration(seconds: number): string {
  if (!seconds || seconds <= 0) return "0s";
  if (seconds < 1) return "<1s";

  const s = Math.floor(seconds % 60);
  const m = Math.floor((seconds / 60) % 60);
  const h = Math.floor(seconds / 3600);

  let parts: string[] = [];
  if (h > 0) parts.push(`${h}h`);
  if (m > 0) parts.push(`${m}m`);
  if (s > 0) parts.push(`${s}s`);

  return parts.join("") || "0s";
}

export function showIfNonZero(n?: number): string {
  return n && n !== 0 ? String(n) : '';
}

// Compact UTC timestamp for task-row secondary text: `2026-07-02 08:11Z`.
// Accepts epoch seconds (int64 on the wire arrives as either number or
// string via protobuf-ts long_type_string). Returns '' for missing/zero
// so callers can drop the row without a conditional.
export function formatEpochUTC(epoch?: number | string | bigint | null): string {
  if (epoch == null) return '';
  const n = typeof epoch === 'number' ? epoch : Number(epoch);
  if (!Number.isFinite(n) || n <= 0) return '';
  const d = new Date(n * 1000);
  const yyyy = d.getUTCFullYear();
  const mm = String(d.getUTCMonth() + 1).padStart(2, '0');
  const dd = String(d.getUTCDate()).padStart(2, '0');
  const hh = String(d.getUTCHours()).padStart(2, '0');
  const min = String(d.getUTCMinutes()).padStart(2, '0');
  return `${yyyy}-${mm}-${dd} ${hh}:${min}Z`;
}