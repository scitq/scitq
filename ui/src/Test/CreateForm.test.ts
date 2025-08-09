vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, waitFor } from '@testing-library/svelte';
import CreateForm from '../components/CreateForm.svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';


describe('CreateForm', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  const mockFlavors = [
    { flavorName: 'flavor1', region: 'us-east', provider: 'aws' },
  ];
  const mockWorkflows = [
    { name: 'step1' },
  ];
  const mockNewWorkerResponse = [
    { workerId: 'w1', workerName: 'workerOne', jobId: 'j1' }
  ];
  const mockStatusResponse = [
    { workerId: 'w1', status: 'running' }
  ];

  it('should load flavors and workflows on mount', async () => {
    // Mock API functions
    mockApi.getFlavors.mockResolvedValue(mockFlavors);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);

    // Render the component
    render(CreateForm);

    // Verify API functions were called
    await waitFor(() => {
      expect(mockApi.getFlavors).toHaveBeenCalled();
      expect(mockApi.getWorkFlow).toHaveBeenCalled();
    });
  });

  it('should correctly fill and update form fields', async () => {
    // Mock API functions
    mockApi.getFlavors.mockResolvedValue(mockFlavors);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);

    // Render the component
    const { getByLabelText } = render(CreateForm);

    // Wait for data to load
    await waitFor(() => {
      expect(mockApi.getFlavors).toHaveBeenCalled();
    });

    // Fill and verify form fields
    const concurrencyInput = getByLabelText('Concurrency:') as HTMLInputElement;
    await fireEvent.input(concurrencyInput, { target: { value: '5' } });
    expect(concurrencyInput.value).toBe('5');

    const prefetchInput = getByLabelText('Prefetch:') as HTMLInputElement;
    await fireEvent.input(prefetchInput, { target: { value: '10' } });
    expect(prefetchInput.value).toBe('10');

    const flavorInput = getByLabelText('Flavor:') as HTMLInputElement;
    await fireEvent.input(flavorInput, { target: { value: 'flavor1' } });
    expect(flavorInput.value).toBe('flavor1');

    const stepInput = getByLabelText('Step (Workflow.step):') as HTMLInputElement;
    await fireEvent.input(stepInput, { target: { value: 'step1' } });
    expect(stepInput.value).toBe('step1');
  });

  it('should call newWorker with correct parameters when form is submitted', async () => {
    // Mock API functions
    mockApi.getFlavors.mockResolvedValue(mockFlavors);
    mockApi.getWorkFlow.mockResolvedValue(mockWorkflows);
    mockApi.newWorker.mockResolvedValue(mockNewWorkerResponse);
    mockApi.getStatus.mockResolvedValue([]);

    // Render the component
    const { getByLabelText, getByTestId } = render(CreateForm);

    // Fill form fields
    await fireEvent.input(getByLabelText('Concurrency:'), { target: { value: '5' } });
    await fireEvent.input(getByLabelText('Prefetch:'), { target: { value: '10' } });
    await fireEvent.input(getByLabelText('Flavor:'), { target: { value: 'flavor1' } });
    await fireEvent.input(getByLabelText('Region:'), { target: { value: 'us-east' } });
    await fireEvent.input(getByLabelText('Provider:'), { target: { value: 'aws' } });
    await fireEvent.input(getByLabelText('Step (Workflow.step):'), { target: { value: 'step1' } });
    await fireEvent.input(getByLabelText('Number:'), { target: { value: '3' } });

    // Submit the form
    await fireEvent.click(getByTestId('add-worker-button'));

    // Verify newWorker was called with correct arguments
    await waitFor(() => {
      expect(mockApi.newWorker).toHaveBeenCalledWith(
        5, 10, 'flavor1', 'us-east', 'aws', 3, 'step1'
      );
    });
  });
});