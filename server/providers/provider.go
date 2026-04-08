package providers

import "errors"

// Provider defines the methods that a cloud provider must implement.
type Provider interface {
	Create(workerName, flavor, location string, jobId int32) (string, error)
	List(location string) (map[string]string, error)
	Restart(workerName, location string) error
	Delete(workerName, location string) error
}

// ErrInstanceLimitReached is returned when a deployment fails due to
// an instance-count limit (e.g. Azure PublicIPCountLimitReached).
// The job queue uses this to learn the limit and stop further attempts.
var ErrInstanceLimitReached = errors.New("instance limit reached")
