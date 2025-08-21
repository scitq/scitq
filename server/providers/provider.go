package providers

// Provider defines the methods that a cloud provider must implement.
type Provider interface {
	Create(workerName, flavor, location string, jobId uint32) (string, error)
	List() (map[string]string, error)
	Restart(workerName string) error
	Delete(workerName string) error
}
