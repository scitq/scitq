vi.mock('../lib/api', () => mockApi);
import { mockApi } from '../mocks/api_mock';

import { render, fireEvent, screen, waitFor } from '@testing-library/svelte';
import { describe, it, expect, vi, beforeEach } from 'vitest';
import WorkflowTemplatePage from '../pages/WfTemplatePage.svelte';
import { wsClient } from '../lib/wsClient';

let messageHandler: (msg: any) => void;

const mockTemplates = [
  {
    workflowTemplateId: 1,
    name: 'Template A',
    version: '1.0',
    paramJson: JSON.stringify([{ name: 'param1', type: 'string', required: true }]),
    uploadedAt: '2023-01-01T00:00:00Z'
  },
  {
    workflowTemplateId: 2,
    name: 'Template B',
    version: '2.0', 
    paramJson: JSON.stringify([{ name: 'param2', type: 'int', required: false }]),
    uploadedAt: '2023-01-02T00:00:00Z'
  }
];

describe('WorkflowTemplatePage', () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mockApi.getTemplates.mockResolvedValue(mockTemplates);
    mockApi.UploadTemplates.mockResolvedValue({ success: true });
    mockApi.runTemp.mockResolvedValue({});

    vi.spyOn(wsClient, 'subscribeToMessages').mockImplementation((handler : any) => {
        messageHandler = handler; // Store the handler for later use
        return () => true; // Unsubscribe function
    });
  });

  it('loads and displays templates on mount', async () => {
    render(WorkflowTemplatePage);
    
    await waitFor(() => {
      expect(screen.getByText('Template A')).toBeInTheDocument();
      expect(screen.getByText('Template B')).toBeInTheDocument();
      expect(mockApi.getTemplates).toHaveBeenCalledTimes(1);
    });
  });

  it('handles file selection and upload', async () => {
    const { getByTestId, findByText, getByTitle } = render(WorkflowTemplatePage);
    
    // Create test file
    const file = new File(['dummy content'], 'test-template.json', { type: 'application/json' });

    // Find file input via parent container
    const fileContainer = getByTestId('wfTemp-page');
    const fileInput = fileContainer.querySelector('input[type="file"]') as HTMLInputElement;
    expect(fileInput).not.toBeNull();

    // Simulate file selection
    await fireEvent.change(fileInput, { 
        target: { 
            files: [file] 
        } 
    });

    // Verify filename appears in the text input
    const fileNameDisplay = await waitFor(() => {
        const display = fileContainer.querySelector('.wfTemp-file-display') as HTMLInputElement;
        expect(display.value).toBe('test-template.json');
        return display;
    });

    // Verify validate button is enabled
    const validateButton = getByTitle('Validate');
    expect(validateButton).not.toBeDisabled();

    // Simulate clicking validate button
    await fireEvent.click(validateButton);

    // Verify API call
    await waitFor(() => {
        expect(mockApi.UploadTemplates).toHaveBeenCalledWith(expect.any(Uint8Array), false);
    });

    // Simulate WebSocket response as sent by server
    if (messageHandler) {
        messageHandler({
            type: 'template-uploaded',
            payload: {
                workflowTemplateId: 123,
                name: 'test-template',
                version: '1.0',
                description: 'Test template',
                paramJson: '[]',
                uploadedAt: new Date().toISOString()
            }
        });
    }

    // Verify new template appears in the list
    expect(await findByText('test-template')).toBeInTheDocument();
  });

  it('opens parameter modal with correct template', async () => {
    render(WorkflowTemplatePage);
    
    await waitFor(() => screen.getByText('Template A'));
    await fireEvent.click(screen.getAllByTitle('Play')[0]);
    
    expect(await screen.findByText('Run "Template A"')).toBeInTheDocument();
    expect(screen.getByLabelText('param1')).toBeInTheDocument();
  });

  it('validates required parameters before running', async () => {
    render(WorkflowTemplatePage);
    
    await waitFor(() => screen.getByText('Template A'));
    await fireEvent.click(screen.getAllByTitle('Play')[0]);
    
    // Try to run without filling required param
    await fireEvent.click(screen.getByText('Run'));
    
    expect(await screen.findByText('This field is required')).toBeInTheDocument();
    expect(mockApi.runTemp).not.toHaveBeenCalled();
  });

  it('sorts templates correctly', async () => {
    render(WorkflowTemplatePage);
    
    await waitFor(() => screen.getByText('Template A'));
    
    // Sort by version (descending)
    await fireEvent.change(screen.getByLabelText('Sort by'), { 
      target: { value: 'version' } 
    });
    
    const templates = screen.getAllByTestId(/^wfTemplate-/);
    expect(templates[0]).toHaveTextContent('Template B'); // v2.0 first
  });

  it('shows error modal on upload failure', async () => {
    // Configure mock to return error response
    mockApi.UploadTemplates.mockResolvedValue({
        success: false,
        message: 'Invalid file format'
    });

    // Render component
    render(WorkflowTemplatePage);

    // Simulate file selection
    const file = new File(['content'], 'test.json', { type: 'application/json' });
    
    // Find file input element
    const fileInput = screen.getByTestId('wfTemp-page')
                            .querySelector('input[type="file"]') as HTMLInputElement;
    await fireEvent.change(fileInput, { target: { files: [file] } });

    // Verify file is selected
    const fileNameDisplay = await screen.findByDisplayValue('test.json');
    expect(fileNameDisplay).toBeInTheDocument();

    // Trigger upload
    await fireEvent.click(screen.getByTitle('Validate'));

    // Verify error modal appears
    await waitFor(() => {
        expect(screen.getByText('Upload Error')).toBeInTheDocument();
        expect(screen.getByText('Invalid file format')).toBeInTheDocument();
    });

    // Verify API was called
    expect(mockApi.UploadTemplates).toHaveBeenCalled();
  });
});