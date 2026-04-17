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

// ErrUnsupportedFlavor is returned when a deployment fails because the
// selected flavor/VM-size is permanently incompatible with the provider
// configuration (e.g. Azure confidential-compute VMs requiring a specific
// securityType that scitq does not set). The job queue uses this to blacklist
// the flavor for that provider/region so the recruiter skips it.
var ErrUnsupportedFlavor = errors.New("unsupported flavor")
