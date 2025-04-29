// import { render, fireEvent, waitFor, screen } from '@testing-library/svelte';
// import { describe, it, expect, vi, beforeEach } from 'vitest';
// import CreateForm from './createForm.svelte';
// import WorkerComponent from './WorkerCompo.svelte';

// vi.mock('../lib/api', () => ({
//   getFlavors: vi.fn().mockResolvedValue([
//     { flavorName: 'small', region: 'eu-west', provider: 'aws' },
//     { flavorName: 'medium', region: 'eu-west', provider: 'gcp' },
//   ]),
//   newWorker: vi.fn(),
//   getWorkers: vi.fn(),
// }));

// import { newWorker, getWorkers } from '../lib/api';

// describe('CreateForm', () => {
//   beforeEach(() => {
//     vi.clearAllMocks(); // Réinitialiser les mocks avant chaque test
//   });

//   it('should call newWorker with form values when Add button is clicked and update the worker list with new worker', async () => {
//     const { getByTestId, container } = render(CreateForm);

//     // Attendre le chargement des flavors
//     await waitFor(() => container.querySelector('#flavor'));

//     // Renseigner les champs du formulaire par ID
//     const concurrencyInput = container.querySelector('#concurrency') as HTMLInputElement;
//     const prefetchInput = container.querySelector('#prefetch') as HTMLInputElement;
//     const flavorInput = container.querySelector('#flavor') as HTMLInputElement;
//     const regionInput = container.querySelector('#region') as HTMLInputElement;
//     const providerInput = container.querySelector('#provider') as HTMLInputElement;
//     const numberInput = container.querySelector('#number') as HTMLInputElement;
//     const taskInput = container.querySelector('#task') as HTMLInputElement;

//     // Simuler la saisie dans les champs du formulaire
//     await fireEvent.input(concurrencyInput, { target: { value: '4' } });
//     await fireEvent.input(prefetchInput, { target: { value: '2' } });
//     await fireEvent.input(flavorInput, { target: { value: 'small' } });
//     await fireEvent.input(regionInput, { target: { value: 'eu-west' } });
//     await fireEvent.input(providerInput, { target: { value: 'aws' } });
//     await fireEvent.input(numberInput, { target: { value: '3' } });
//     await fireEvent.input(taskInput, { target: { value: 'task-xyz' } });

//     // Clic sur le bouton "Add"
//     const addButton = getByTestId('add-worker-button');
//     await fireEvent.click(addButton);

//     // Mocker la réponse de getWorkers après l'ajout du worker
//     const newWorkerData = { workerId: 'def456', name: 'worker-new', status: 'P' };
//     (getWorkers as any).mockResolvedValueOnce([
//       { workerId: 'abc123', name: 'worker-old', status: 'P' }, // Ancien worker
//       newWorkerData, // Worker nouvellement ajouté
//     ]);

//     // Vérification que newWorker a été appelé avec les bons paramètres
//     await waitFor(() => {
//       expect(newWorker).toHaveBeenCalledWith(4, 2, 'small', 'eu-west', 'aws', 3);
//     });
//   });

//   it('should call newWorker and then display the new worker in the list', async () => {
//     // Mock la création du worker
//     (newWorker as any).mockResolvedValueOnce({ workerId: 'new999' });

//     // Mock la liste des workers après ajout
//     (getWorkers as any).mockResolvedValueOnce([
//       { workerId: 'new999', name: 'worker-added', status: 'P' },
//     ]);

//     render(WorkerComponent); // ou le composant qui inclut le bouton d'ajout et l'affichage de la liste

//     // Simuler l'interaction d'ajout — adapte ce bloc selon ta vraie UI :
//     const addButton = await screen.findByTestId('add-worker-button');
//     await fireEvent.click(addButton);

//     // Vérifie que le worker ajouté apparaît dans la liste
//     await waitFor(() => {
//       expect(screen.getByText(/worker-added/i)).toBeInTheDocument();
//     });
//   });


// });
