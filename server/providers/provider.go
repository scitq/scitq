package providers

import "errors"

// Provider defines the methods that a cloud provider must implement.
//
// `hasGPU` on Create lets the provider pick a different VM image for
// GPU flavors. Today scitq supports a single CPU image plus an
// optional `gpu_image` override (see AzureConfig.GPUImage,
// OpenstackConfig.GPUImageID); when has_gpu=true and `gpu_image` is
// configured, the provider boots that image instead of the default.
// The flag is passed by the caller (jobqueue) — it was already
// selected from the flavor catalog at recruit time, so the provider
// just consumes it.
type Provider interface {
	Create(workerName, flavor, location string, hasGPU bool, jobId int32) (string, error)
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
