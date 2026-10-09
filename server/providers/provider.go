package providers

import "errors"

// CreateParams carries every per-recruiter knob that affects a Create
// call. Grouping them into a struct keeps the Provider.Create signature
// stable as new knobs are added: a provider that doesn't support a
// field reads it as the zero value and either ignores it (if the zero
// value is a sensible no-op) or returns a clear "not supported" error.
//
// Image selection has three sources, in precedence order:
//   1. GPUImage (when HasGPU=true) — per-recruiter override from the
//      workflow's worker_pool.gpu_image. Empty string falls through.
//   2. Image (regardless of HasGPU) — per-recruiter override from
//      worker_pool.image. Empty string falls through.
//   3. scitq.yaml provider config — AzureConfig.Image /
//      AzureConfig.GPUImage / OpenstackConfig.ImageID /
//      OpenstackConfig.GPUImageID. The provider picks GPUImage when
//      HasGPU=true, else Image.
//
// Format of the image strings is provider-specific (Azure:
// "publisher/offer/sku/version"; OpenStack: image name or UUID). Each
// provider parses its own value and errors at Create time on malformed
// input — the recruiter doesn't validate format.
//
// SwapProportion is the per-recruiter swapfile sizing override. nil =
// fall back to the server-wide cfg.Scitq.SwapProportion default. A
// non-nil value is embedded in the cloud-init as the `-swap X`
// argument to `scitq-client -install`; `-swap 0.0` disables swap.
//
// ExtraStorageGB > 0 asks the provider to provision an extra block
// volume of that size and mount it at /scratch BEFORE scitq-client
// installs. Providers that don't support this (currently Azure) MUST
// return a clear error when ExtraStorageGB > 0 rather than silently
// ignoring it (so the user sees the mismatch at recruit time, not as
// a mystery /scratch-full failure hours later). ExtraStorageType is a
// provider-specific volume class name (OVH: "classic",
// "high-speed", "high-speed-gen2"); nil → provider default.
type CreateParams struct {
	WorkerName       string
	Flavor           string
	Location         string
	HasGPU           bool
	Image            string
	GPUImage         string
	JobID            int32
	SwapProportion   *float32
	ExtraStorageGB   *int32
	ExtraStorageType *string
}

// CreateResult is the return shape of Provider.Create. IP is the public
// (or best-available private) address the server records on the worker
// row. ExtraVolumeID is the provider's volume handle when the request
// included extra storage — persisted on worker.extra_storage_volume_id
// so the delete path (and the orphan-volume janitor) can detach and
// destroy the volume even if the VM is already gone. Empty when the
// request didn't ask for extra storage.
type CreateResult struct {
	IP            string
	ExtraVolumeID string
}

type Provider interface {
	Create(params CreateParams) (CreateResult, error)
	List(location string) (map[string]string, error)
	Restart(workerName, location string) error
	Delete(workerName, location string) error
}

// ResolveSwapProportion picks the swapfile sizing to bake into a
// worker's cloud-init: a non-nil per-recruiter override wins, otherwise
// fall back to the server-wide config default. Shared across providers
// so Azure and Openstack resolve the override identically.
func ResolveSwapProportion(override *float32, configDefault float32) float32 {
	if override != nil {
		return *override
	}
	return configDefault
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
