// The worker events panel pages through a worker's events and filters
// them by level. The fake server holds 250 events, newest first; one in
// five is a warning, one in fifty an error.
import { mockApi } from '../mocks/api_mock';
import { render, fireEvent, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import WorkerCompo from '../components/WorkerCompo.svelte';
import { settle } from './wsHarness';

const events = Array.from({ length: 250 }, (_, i) => {
  const eventId = 250 - i;
  const level = eventId % 50 === 0 ? 'E' : eventId % 5 === 0 ? 'W' : 'I';
  return { eventId, level, createdAt: `t${eventId}`, eventClass: 'phase', message: `event ${eventId}` };
});

const shown = () => document.querySelectorAll('.worker-events-row');

beforeEach(async () => {
  vi.useFakeTimers();
  mockApi.getWorkers.mockResolvedValue([{ workerId: 1, name: 'w1', status: 'R', concurrency: 1, prefetch: 0, pendingWarnings: 50 }]);
  mockApi.getAllTaskStats.mockResolvedValue({ perWorkerStatusCounts: {}, globalStatusCounts: {}, totalCount: 0 });
  mockApi.listWorkerEvents.mockImplementation(async (_wid: number, limit: number, f: { level?: string; beforeEventId?: number } = {}) =>
    events.filter((e) => (!f.level || e.level === f.level) && (f.beforeEventId === undefined || e.eventId < f.beforeEventId)).slice(0, limit));
  render(WorkerCompo);
  await settle();
  await fireEvent.click(document.querySelector('.worker-events-badge')!);
  await settle();
});

afterEach(() => {
  vi.useRealTimers();
});

describe('worker events panel', () => {
  it('loads older events page by page until none remain', async () => {
    expect(shown()).toHaveLength(100);
    await fireEvent.click(screen.getByTestId('events-load-more'));
    await settle();
    expect(shown()).toHaveLength(200);
    await fireEvent.click(screen.getByTestId('events-load-more'));
    await settle();
    expect(shown()).toHaveLength(250);
    expect(shown()[249].textContent).toContain('event 1');
    expect(screen.queryByTestId('events-load-more')).toBeNull();
  });

  it('shows only warnings after a click on the unread warnings count', async () => {
    await fireEvent.click(screen.getByTestId('events-unread-warnings'));
    await settle();
    expect(shown()).toHaveLength(45);
    expect([...shown()].every((r) => r.classList.contains('worker-events-level-W'))).toBe(true);
    expect(screen.queryByTestId('events-load-more')).toBeNull();
  });

  it('shows only errors, and all events again', async () => {
    await fireEvent.click(screen.getByTestId('events-level-E'));
    await settle();
    expect(shown()).toHaveLength(5);
    await fireEvent.click(screen.getByTestId('events-level-all'));
    await settle();
    expect(shown()).toHaveLength(100);
  });
});
