vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import TaskList from '../components/TaskList.svelte';

// Mock data injected via prop
const mockTasks = [
  {
    taskId: 1,
    name: 'Task A',
    command: 'echo A',
    workerId: 'Worker-1',
    stepId: 10,
    status: 1,
    runningTimeout: '30s',
    output: 'OK',
    retry: 0
  },
  {
    taskId: 2,
    name: 'Task B',
    command: 'echo B',
    workerId: 'Worker-2',
    stepId: 11,
    status: 2,
    runningTimeout: '45s',
    output: 'OK',
    retry: 1
  },
];

describe('TaskList', () => {
  it('displays tasks in the table', async () => {
    render(TaskList, { tasks: mockTasks });

    // Checks that rows are properly rendered
    const rows = await screen.findAllByTestId(/^task-/);
    expect(rows.length).toBe(2); // 2 mocked tasks

    // Checks that contents are visible
    expect(screen.getByText('Task A')).toBeInTheDocument();
    expect(screen.getByText('Task B')).toBeInTheDocument();
    expect(screen.getByText('echo A')).toBeInTheDocument();
    expect(screen.getByText('echo B')).toBeInTheDocument();
  });

  it('displays a message if no tasks are present', async () => {
    render(TaskList, { tasks: [] });

    expect(screen.getByText('No task found.')).toBeInTheDocument();
  });
});
