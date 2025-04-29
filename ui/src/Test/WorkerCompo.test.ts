import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import WorkerCompo from '../components/WorkerCompo.svelte';

// Mock all functions from ../lib/api
vi.mock('../lib/api', () => {
  return {
    getWorkers: vi.fn(),
    updateWorkerConfig: vi.fn(),
    delWorker: vi.fn(),
    getStatusClass: (status: string) => status,
    getStatusText: (status: string) => status,
  };
});

import { getWorkers, updateWorkerConfig, delWorker } from '../lib/api';

// Mocked workers data
const mockWorkers = [
  {
    workerId: 'worker-1',
    name: 'Worker One',
    batch: 10,
    status: 'R',
    concurrency: 5,
    prefetch: 10,
    accepted: 5,
    running: 3,
    successes: 20,
    fail: 2,
    cpuUsage: 50,
    memUsage: 70,
    loadAvg: 1.5,
    diskUsage: 80,
    diskRW: "10MB/5MB",
    netIO: "100MB/50MB",
  },
];

describe('WorkerCompo', () => {
  beforeEach(() => {
    vi.clearAllMocks();
  });

  it('should display the list of workers', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);

    const { getByText } = render(WorkerCompo);

    await waitFor(() => {
      expect(getByText('Worker One')).toBeTruthy();
    });
  });

    it('should display "No workers found." when no jobs are available', async () => {
      (getWorkers as any).mockResolvedValue([]);
  
      const { getByText } = render(WorkerCompo);
  
      await waitFor(() => {
        expect(getByText('No workers found.')).toBeTruthy();
      });
    });

  it('should increase a worker\'s concurrency', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);

    const { getByTestId } = render(WorkerCompo);

    await waitFor(() => getByTestId('increase-concurrency-worker-1'));

    const plusButton = getByTestId('increase-concurrency-worker-1');
    await fireEvent.click(plusButton);

    await waitFor(() => {
      expect(updateWorkerConfig).toHaveBeenCalledWith('worker-1', 6, 10);
    });
  });

  it('should decrease a worker\'s prefetch', async () => {
    (getWorkers as any).mockResolvedValue(mockWorkers);
  
    const { getByTestId } = render(WorkerCompo);
  
    await waitFor(() => getByTestId('decrease-prefetch-worker-1'));

    const prefetchMinusButton = getByTestId('decrease-prefetch-worker-1');
    // Set the concurrency to a known value for the test
    mockWorkers[0].concurrency = 5; // Explicitly set concurrency to 5
    
    await fireEvent.click(prefetchMinusButton);
  
    // Attendre que la fonction updateWorkerConfig ait bien été appelée
    await waitFor(() => {
      expect(updateWorkerConfig).toHaveBeenCalledWith('worker-1', 5, 9);
    });
  });
  

  it('should delete a worker', async () => {
    // Render the component
    const { getByText } = render(WorkerCompo);

    // Assurer-toi que le worker est bien rendu
    await waitFor(() => getByText('Worker One'));

    // Cherche le bouton de suppression avec le bon test ID
    const deleteButton = await screen.findByTestId('delete-worker-worker-1');
    
    // Clique sur le bouton de suppression
    await fireEvent.click(deleteButton);

    // Vérifie que la fonction `delWorker` a été appelée avec le bon argument
    await waitFor(() => {
      expect(delWorker).toHaveBeenCalledWith({ workerId: 'worker-1' });
    });
  });
});
