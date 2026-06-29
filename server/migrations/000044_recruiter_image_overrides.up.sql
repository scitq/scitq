-- Per-recruiter cloud-image overrides.
--
-- Today the cloud image booted for a recruited worker comes from
-- server-wide config (scitq.yaml: azure.image / azure.gpu_image,
-- openstack.image_id / openstack.gpu_image_id). That's enough when
-- every recruiter on the deployment wants the same image, but it
-- breaks down once different workflows need different images:
--
--   * one pipeline pins to a custom-baked image with a baked-in tool;
--   * a GPU pipeline on Azure NV family needs the vGPU/Grid image
--     instead of the default HPC image (the alpha2 2026-06-25
--     incident — NC's HPC image hit "nvml: driver not loaded" on
--     NVads_A10_v5);
--   * a step uses an ARM-native image alongside x86 default pools.
--
-- Two columns, paralleling the server-config split:
--
--   * image:     override applied to any flavor the recruiter picks
--   * gpu_image: override applied ONLY to flavors with has_gpu=TRUE
--
-- Precedence at deploy time: gpu_image (if has_gpu) > image (if set) >
-- server-config default. NULL on either column means "fall through".
--
-- Format is provider-specific (Azure: "publisher/offer/sku/version";
-- OpenStack: image name or UUID); each provider parses its own column
-- value. We use TEXT to stay format-agnostic at the DB layer.
ALTER TABLE recruiter
  ADD COLUMN image     TEXT NULL,
  ADD COLUMN gpu_image TEXT NULL;
