import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import CreateForm from '../components/CreateForm.svelte';
import Dashboard from '../pages/Dashboard.svelte';

vi.mock('../lib/api', async () => {
  const actual = await vi.importActual<typeof import('../lib/api')>('../lib/api');
  return {
    ...actual,
    getFlavors: vi.fn().mockResolvedValue([
      { flavorName: 'small', region: 'eu-west', provider: 'aws' },
    ]),
    newWorker: vi.fn(),
    getWorkers: vi.fn(),
  };
});

import { newWorker, getWorkers } from '../lib/api';

describe('CreateForm', () => {
  beforeEach(() => {
    vi.clearAllMocks(); // Reset all mocks before each test
  });

  it('should load flavors and display matching suggestions for flavor, region, and provider', async () => {
    const { container } = render(CreateForm);

    // Wait for the flavor input to appear (which implies flavors are loaded)
    await waitFor(() => expect(container.querySelector('#flavor')).toBeInTheDocument());

    const flavorInput = container.querySelector('#flavor') as HTMLInputElement;
    const regionInput = container.querySelector('#region') as HTMLInputElement;
    const providerInput = container.querySelector('#provider') as HTMLInputElement;

    // Test flavor autocomplete
    await fireEvent.input(flavorInput, { target: { value: 'sma' } });
    await waitFor(() => {
      expect(screen.getByText('small')).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText('small'));
    expect(flavorInput.value).toBe('small');

    // Test region autocomplete
    await fireEvent.input(regionInput, { target: { value: 'eu' } });
    await waitFor(() => {
      expect(screen.getByText('eu-west')).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText('eu-west'));
    expect(regionInput.value).toBe('eu-west');

    // Test provider autocomplete
    await fireEvent.input(providerInput, { target: { value: 'aw' } });
    await waitFor(() => {
      expect(screen.getByText('aws')).toBeInTheDocument();
    });
    await fireEvent.click(screen.getByText('aws'));
    expect(providerInput.value).toBe('aws');
  });

  it('should call newWorker with form values when Add button is clicked', async () => {
    const { getByTestId, container } = render(CreateForm);

    // Wait for the form to fully render
    await waitFor(() => container.querySelector('#flavor'));

    // Get form inputs by ID
    const concurrencyInput = container.querySelector('#concurrency') as HTMLInputElement;
    const prefetchInput = container.querySelector('#prefetch') as HTMLInputElement;
    const flavorInput = container.querySelector('#flavor') as HTMLInputElement;
    const regionInput = container.querySelector('#region') as HTMLInputElement;
    const providerInput = container.querySelector('#provider') as HTMLInputElement;
    const numberInput = container.querySelector('#number') as HTMLInputElement;
    const taskInput = container.querySelector('#task') as HTMLInputElement;

    // Fill the form inputs
    await fireEvent.input(concurrencyInput, { target: { value: '4' } });
    await fireEvent.input(prefetchInput, { target: { value: '2' } });
    await fireEvent.input(flavorInput, { target: { value: 'small' } });
    await fireEvent.input(regionInput, { target: { value: 'eu-west' } });
    await fireEvent.input(providerInput, { target: { value: 'aws' } });
    await fireEvent.input(numberInput, { target: { value: '3' } });
    await fireEvent.input(taskInput, { target: { value: 'task-xyz' } });

    // Click the "Add" button
    const addButton = getByTestId('add-worker-button');
    await fireEvent.click(addButton);

    // Mock getWorkers response after submission
    const newWorkerData = { workerId: 'def456', name: 'worker-new', status: 'P' };
    (getWorkers as any).mockResolvedValueOnce([
      { workerId: 'abc123', name: 'worker-old', status: 'P' },
      newWorkerData,
    ]);

    // Expect the newWorker API call to be made with correct arguments
    await waitFor(() => {
      expect(newWorker).toHaveBeenCalledWith(4, 2, 'small', 'eu-west', 'aws', 3);
    });
  });

  it('should call newWorker and refresh the worker list', async () => {
    // Mock creation of a new worker
    (newWorker as any).mockResolvedValueOnce({ workerId: 'new999' });

    // Mock updated worker list
    (getWorkers as any).mockResolvedValueOnce([
      { workerId: 'new999', name: 'worker-added', status: 'P' },
    ]);

    render(Dashboard);

    // Click on the Add button from the dashboard
    const addButton = await screen.findByTestId('add-worker-button');
    await fireEvent.click(addButton);

    // Wait for the new worker to appear in the DOM
    await waitFor(() => {
      expect(screen.getByTestId('worker-new999')).toBeInTheDocument();
    });
  });
});
