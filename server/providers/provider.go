package providers

import "errors"

// Provider defines the methods that a cloud provider must implement.
//
// Image selection on Create has three sources, in precedence order:
//   1. `gpuImage` parameter (when hasGPU=true) — per-recruiter override
//      from the workflow's worker_pool.gpu_image. Empty string falls
//      through.
//   2. `image` parameter (regardless of hasGPU) — per-recruiter
//      override from worker_pool.image. Empty string falls through.
//   3. scitq.yaml provider config — AzureConfig.Image /
//      AzureConfig.GPUImage / OpenstackConfig.ImageID /
//      OpenstackConfig.GPUImageID. The provider picks GPUImage when
//      hasGPU=true, else Image.
//
// Format of the image strings is provider-specific (Azure:
// "publisher/offer/sku/version"; OpenStack: image name or UUID). Each
// provider parses its own value and errors at Create time on malformed
// input — the recruiter doesn't validate format.
type Provider interface {
	Create(workerName, flavor, location string, hasGPU bool, image, gpuImage string, jobId int32) (string, error)
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
