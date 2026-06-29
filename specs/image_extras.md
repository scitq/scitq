# Per-image cloud-init extras

## Problem

Once `worker_pool.image` / `worker_pool.gpu_image` lets the workflow
author pick a non-default cloud image, the new image's gaps become
the workflow author's problem. Each image fills a different slice of
the stack:

| Image | Driver | Container runtime | Other |
|---|---|---|---|
| `microsoft-dsvm/ubuntu-hpc/2204` (NC default) | datacenter | nvidia-container-toolkit pre-installed | docker pre-configured for `--gpus` |
| `nvidia/nvidia-vws-ubuntu-24-lts/...` (NV Grid) | Grid (vGPU) | **NOT installed** | needs NLS license server config |
| `Canonical/UbuntuServer/24.04-LTS/...` (plain) | none | not installed | needs `NvidiaGpuDriverLinux` extension |
| `microsoft-dsvm/ubuntu-hpc/2404-rocm` (AMD) | ROCm | rocm-container-runtime needed | different docker runtime |

The Azure provider's cloud-init can't bake assumptions about NVIDIA
(or any specific stack) into the generic path: doing so installs
duplicate packages on images that already ship them, ships wrong
packages on AMD/ROCm images, and grows an unmaintainable matrix of
"if image X then install Y" branches inside the provider code (the
alpha2 2026-06-29 incident with the unconditional toolkit install
made this concrete).

The image override mechanism needs a companion that says: "when this
image is picked, ALSO run these cloud-init steps." The operator who
chose the image is the one who knows what it lacks.

## Solution

Extend `worker_pool` (workflow side) and the provider config
(server side) to carry **image extras** alongside every image
override. An "extra" is a list of cloud-init `runcmd` entries
appended verbatim to the generic VM cloud-init.

```yaml
worker_pool:
  gpu: true
  flavor: "Standard_NV%"
  gpu_image: "nvidia/nvidia-vws-ubuntu-24-lts/ubuntu24lts_with_vgpu18-0/20.0.0"
  gpu_image_extras:
    - >-
        command -v nvidia-ctk >/dev/null 2>&1
        || ( curl -fsSL https://nvidia.github.io/libnvidia-container/gpgkey
        | gpg --dearmor -o /usr/share/keyrings/nvidia-container-toolkit-keyring.gpg
        && curl -fsSL https://nvidia.github.io/libnvidia-container/stable/deb/nvidia-container-toolkit.list
        | sed 's|deb https|deb [signed-by=/usr/share/keyrings/nvidia-container-toolkit-keyring.gpg] https|' > /etc/apt/sources.list.d/nvidia-container-toolkit.list
        && apt-get update -q && apt-get install -y -q nvidia-container-toolkit
        && nvidia-ctk runtime configure --runtime=docker && systemctl restart docker )
    - echo NV-image-extras applied
```

### Field shape

Two new fields parallel `image` / `gpu_image`:

| field | applies when | type |
|---|---|---|
| `image_extras` | any flavor — paired with `image` | list of strings (runcmd lines) |
| `gpu_image_extras` | flavors with `has_gpu=TRUE` — paired with `gpu_image` | list of strings |

Each list entry becomes one `runcmd:` entry in the generated
cloud-init. Empty list / unset = no extras. Format mirrors
cloud-init's `runcmd:` — folded scalars (`>-`) are fine, single-line
commands are fine.

### Server-config layer

The same shape lives in `scitq.yaml` per provider:

```yaml
azure:
  images:
    - match: "Standard_NV%"
      image: nvidia/nvidia-vws-ubuntu-24-lts/ubuntu24lts_with_vgpu18-0/20.0.0
      extras:
        - command -v nvidia-ctk >/dev/null 2>&1 || ( ... toolkit install ... )
    - match: "Standard_NC%"
      image: microsoft-dsvm/ubuntu-hpc/2204/latest
      # No extras — image ships everything.
    - image: Canonical/UbuntuServer/24.04-LTS/latest
      # CPU default — no extras.
```

This is the natural extension of the rule-list spec
([cloud_image_picker.md](cloud_image_picker.md)): each `ImageRule`
gains an optional `extras: []string` field. The picker returns
`(image, extras)` instead of just `image`.

### Composition with per-recruiter overrides

When the workflow author sets both per-pool image AND extras, both
ship together — the picker isn't consulted, the recruiter row carries
the image string + the extras list verbatim. When the workflow author
sets ONLY the image (not extras), the server-config picker runs on
the chosen image string to find matching extras (so an operator can
register "use these extras whenever someone picks the NVIDIA VWS
image" globally without each workflow author having to remember). The
matching rule for that lookup uses the same LIKE semantics as
`ImageRule.match`, applied to the image string.

Precedence:
1. **Per-recruiter `image_extras` / `gpu_image_extras`** (workflow
   override). Replaces server-config extras entirely — the workflow
   author committed to specifying.
2. **Server-config rule extras** (matched by image string LIKE).
3. **No extras** — generic cloud-init only.

## Wire format

### Migration

`recruiter.image_extras TEXT[] NULL` and `recruiter.gpu_image_extras
TEXT[] NULL` — Postgres TEXT arrays, NULL = "fall through to server
config".

### Proto

`Recruiter` / `RecruiterUpdate` / `WorkerRequest`:

```proto
repeated string image_extras     = 18;
repeated string gpu_image_extras = 19;
```

`WorkerRequest` carries them from `deployWorkers` into `CreateWorker`,
the handler stamps them on the `Job` struct, jobqueue passes them
into `Provider.Create()`. Same shape as the existing `image` /
`gpu_image` plumbing.

### Provider interface

```go
Create(workerName, flavor, location string, hasGPU bool,
       image, gpuImage string,
       imageExtras, gpuImageExtras []string,
       jobId int32) (string, error)
```

Each provider then weaves the extras into its cloud-init format.
For Azure, that means appending entries to `runcmd:` in the same
generated `#cloud-config` document.

### Picker extension

`PickImage` returns `(image, extras)`:

```go
type ImageRule struct {
    Match  string   `yaml:"match"`
    HasGPU *bool    `yaml:"has_gpu"`
    Image  string   `yaml:"image"`
    Extras []string `yaml:"extras"`
}

func PickImage(rules []ImageRule, flavorName string, hasGPU bool) (image string, extras []string)
```

Empty list = no extras. First-match-wins semantics unchanged.

## Safety rails

- **No automatic colon-space rewriting.** Cloud-init's YAML loader
  trips on `: ` (colon-space) inside `runcmd:` plain scalars. Each
  extra entry is appended verbatim — if the operator writes a
  poisonous string, the VM fails to install scitq-client and we see
  the same silent-failure mode as the 2026-06-27 worker 6124 incident.
  Mitigation: the cloud-init template wraps each extra in `- >-`
  (folded scalar) when serializing, so the colon-space gotcha is
  neutralized at the structural level. Operators don't have to
  remember the YAML rule.

- **No automatic retry / idempotence.** An extra that's `apt-get
  install foo` runs every boot (idempotent — apt no-ops if already
  installed, ~50ms). If the operator wants a one-shot, they wrap in
  a guard (`command -v foo >/dev/null 2>&1 || apt-get install foo`)
  themselves. The mechanism doesn't try to be clever.

- **No string interpolation.** Extras are NOT a templating system —
  no `{params.x}` substitution, no env expansion. The string is
  what hits cloud-init. If an extra needs a workflow-level value, it
  goes through a YAML `vars:` block at the workflow level and the
  operator builds the literal string before submission.

## Test plan

Phase 1 (pre-implementation): just this spec.

Phase 2 (implementation):

- Unit tests: `PickImage` returns extras alongside image; empty list
  when rule has none; picker first-match semantics preserved.
- Unit tests on a hypothetical `buildCloudInit(extras []string)`
  helper: each entry becomes a `- >- entry`-formatted runcmd line;
  embedded `: ` (colon-space) doesn't break the YAML; entries with
  newlines are folded; empty list produces no extra lines.
- Integration: a smoke test that submits with `gpu_image_extras:
  ["echo HELLO_FROM_EXTRA"]` and asserts the worker's cloud-init log
  contains the echo.

## Migration path

- **Phase 1 (this doc)**: spec only.
- **Phase 2**: implement picker + provider extras in Azure + OpenStack
  + Fake. Document operator examples for NVIDIA VWS + nvidia-container-toolkit
  and NVIDIA AI Enterprise + license-server config. Ship the picker
  extras alongside the `cloud_image_picker.md` Phase 2 work — the two
  features are co-implemented.
- **Phase 3**: revisit whether the "per-rule extras matched by image
  string" lookup is pulling its weight. If every operator ends up
  writing extras at the workflow level anyway (because the server
  config can't reliably know what each image needs), simplify to
  just the per-recruiter path.

## Open questions

1. **Run-once vs. every-boot semantics?** Today scitq workers boot
   once and live until deleted, so the distinction barely matters.
   If we ever ship reboot-safe workers, extras would want a
   "once-only" mode (cloud-init has `bootcmd:` vs `runcmd:` — once
   vs first-boot-only — and a `phone_home` for state). Out of scope
   for Phase 2.

2. **Per-provider extras dialects?** Azure and OpenStack both use
   cloud-init; the extras strings should be portable across them
   for free. Custom providers (Fake, future hyperscalers) might use
   a different bootstrap mechanism — we'd ignore extras on those
   for now and revisit if anyone needs it.

3. **Should the extras list be a YAML structure instead of strings?**
   E.g., `{install: [package-list]}`, `{copy_file: {src, dst}}`, etc.
   Cleaner but invents a new DSL when cloud-init already has one.
   Sticking with raw cloud-init strings keeps the surface area small
   and makes copy-paste from `cloud-init` docs trivial.
