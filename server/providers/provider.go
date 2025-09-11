package providers

// Provider defines the methods that a cloud provider must implement.
type Provider interface {
	Create(workerName, flavor, location string, jobId int32) (string, error)
	List() (map[string]string, error)
	Restart(workerName, location string) error
	Delete(workerName, location string) error
}
