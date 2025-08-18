vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, screen, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi } from 'vitest';
import TaskList from '../components/TaskList.svelte';

const mockTasks = [
  {
    taskId: 1,
    name: 'Task A',
    command: 'echo A',
    workerId: 'Worker-1',
    stepId: 10,
    workflowId: 100,
    status: 1,
    runningTimeout: '30s',
    output: 'Default Output A',
    retry: 0
  },
  {
    taskId: 2,
    name: 'Task B',
    command: 'echo B',
    workerId: 'Worker-2',
    stepId: 11,
    workflowId: 101,
    status: 2,
    runningTimeout: '45s',
    output: 'Default Output B',
    retry: 1
  },
];

const mockTaskLogsSaved = [
  {
    taskId: 1,
    stdout: ['stdout log A1'],
    stderr: ['stderr log A1'],
  },
  {
    taskId: 2,
    stdout: ['stdout log B1', 'stdout log B2'],
    stderr: ['stderr log B1'],
  },
];

describe('TaskList', () => {
  it('displays tasks with output and error logs', async () => {
    render(TaskList, {
      displayedTasks: mockTasks,
      taskLogsSaved: mockTaskLogsSaved,
      workers: [],
      workflows: [],
      allSteps: [],
      onOpenModal: vi.fn(),
    });

    // Verify all task rows are rendered
    const rows = await screen.findAllByTestId(/^task-/);
    expect(rows.length).toBe(2);

    // Check task names and commands are displayed
    expect(screen.getByText('Task A')).toBeInTheDocument();
    expect(screen.getByText('Task B')).toBeInTheDocument();
    expect(screen.getByText('echo A')).toBeInTheDocument();
    expect(screen.getByText('echo B')).toBeInTheDocument();

    // Verify all log outputs are displayed correctly
    expect(screen.getByText((content) => content.includes('stdout log A1'))).toBeInTheDocument();
    expect(screen.getByText((content) => content.includes('stdout log B1'))).toBeInTheDocument();
    expect(screen.getByText((content) => content.includes('stdout log B2'))).toBeInTheDocument();
    expect(screen.getByText((content) => content.includes('stderr log A1'))).toBeInTheDocument();
    expect(screen.getByText((content) => content.includes('stderr log B1'))).toBeInTheDocument();
  });
});