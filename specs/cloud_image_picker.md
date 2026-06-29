# Per-flavor cloud image selection

## Problem

Today each provider's server config carries exactly two cloud-image
defaults:

```yaml
azure:
  image:      { publisher: Canonical, offer: UbuntuServer, sku: 24.04-LTS, version: latest }
  gpu_image:  { publisher: microsoft-dsvm, offer: ubuntu-hpc, sku: 2204, version: latest }
```

At deploy time the provider picks `gpu_image` when the recruited
flavor has `has_gpu=TRUE`, else `image`. This worked when the only
hardware split that mattered was "has a GPU or doesn't." It is
already too coarse:

- **Azure NV family** (vGPU partitioned, Standard_NV* / NVadsA10) needs
  the **Grid driver**, not the datacenter driver shipped with the HPC
  image. Booting HPC on NV gives `nvml error: driver not loaded` —
  observed on alpha2 incident 2026-06-25 (worker 6113).
- **Azure NC family** (full passthrough, Standard_NC*) wants the HPC
  image — datacenter driver, ready for `--gpus all`.
- **Azure NG family** (and future variants) may want yet another image.
- **Future ARM64 instances** on either provider will need an ARM-native
  image, regardless of GPU.
- **Confidential-compute SKUs** need an image with the right enclave
  support.

A single `has_gpu` bool can't disambiguate any of these.

## Solution

Each provider config takes an ordered list of **image rules**. The
provider scans the list at VM creation time and the first rule whose
predicates match the chosen flavor wins. A rule with no predicates is
a fallback default.

```yaml
azure:
  images:
    - match: "Standard_NV%"            # vGPU — needs Grid driver
      image: nvidia/grid-driver/2204/latest
    - match: "Standard_NC%"            # full passthrough — HPC image
      image: microsoft-dsvm/ubuntu-hpc/2204/latest
    - has_gpu: true                    # any other GPU flavor — default GPU image
      image: microsoft-dsvm/ubuntu-hpc/2204/latest
    - match: "%-arm-%"                 # ARM SKUs (future)
      image: Canonical/UbuntuServer/24.04-LTS-arm64/latest
    - image: Canonical/UbuntuServer/24.04-LTS/latest   # default — no predicate
```

### Rule shape

A rule is a YAML map with three optional fields:

| field | type | meaning |
|---|---|---|
| `match` | string | SQL-LIKE pattern matched against `flavor.flavor_name`. `%` = zero-or-more chars; literal chars otherwise. Case-sensitive. Empty / absent = "match any flavor name". |
| `has_gpu` | bool | When set, restricts the rule to flavors with the matching `has_gpu` value. Absent = don't care. |
| `image` | string | The cloud-image string. Format is provider-specific (Azure: `publisher/offer/sku[/version]`; OpenStack: bare name or UUID). **Required** — a rule with no `image` is rejected at config load. |

A rule matches a flavor when **every** set predicate is satisfied
(predicates AND). A rule with no `match` and no `has_gpu` is the
unconditional default — it must be the last entry (or unreachable
entries lurking behind it become silent dead code; the config
validator warns).

### Precedence at deploy time

When `provider.Create()` runs, the image is picked in this order:

1. **Per-recruiter `gpu_image`** (when the chosen flavor has
   `has_gpu=TRUE`) — workflow-level override from
   `worker_pool.gpu_image`. Wins over everything below.
2. **Per-recruiter `image`** — workflow-level override from
   `worker_pool.image`. Wins over server config but loses to a set
   `gpu_image` when the flavor has a GPU.
3. **Server-config `images:` first match** — first rule in the list
   whose predicates match the flavor.
4. **Legacy `image` / `gpu_image` server-config fields** — kept for
   one release as syntactic sugar: when `images:` is unset/empty AND
   the legacy fields are set, the provider synthesizes a 2-entry
   internal list `[{has_gpu: true, image: legacy.gpu_image}, {image:
   legacy.image}]` at load time. Operators who never migrate keep
   today's behaviour. The config validator emits a deprecation
   warning when this fallback fires.
5. **Hardcoded provider fallback** — Azure: `Canonical/UbuntuServer/24.04-LTS/latest`;
   OpenStack: provider-defined. Used only when nothing above resolves
   (misconfigured scitq.yaml).

### Why first-match instead of best-match

`match` patterns can overlap (`Standard_NV%` and `Standard_N%`). A
"best match" rule (longest pattern wins / highest specificity wins)
hides ordering decisions inside the picker; the operator writing
the YAML can't tell which rule will win for a given flavor without
running the picker in their head. First-match is the dumbest rule
that works: the order in YAML is the order of precedence, full stop.

### LIKE semantics

`match` is interpreted as SQL LIKE with one wildcard: `%` matches
zero or more characters. Everything else is literal (including `_`
— flavor names like `Standard_NC24ads_A100_v4` use underscores
liberally; promoting `_` to a wildcard would create accidental
matches). Internally the picker translates the pattern to a regex
(`%` → `.*`; regex specials escaped) anchored at both ends.

### Provider-format opacity

The picker is provider-agnostic — it returns a raw image string.
The Azure provider then parses `publisher/offer/sku[/version]`; the
OpenStack provider treats the string as a bare image name or UUID
and resolves it through Glance. Malformed strings error at VM
creation time (clear failed job in the UI), not at config load —
the picker has no way to know which provider the rule applies to.

## Wire format

### Go (server config)

```go
type ImageRule struct {
    Match  string `yaml:"match"`         // LIKE pattern; "" = match any
    HasGPU *bool  `yaml:"has_gpu"`       // nil = don't care
    Image  string `yaml:"image"`         // required
}

type AzureConfig struct {
    // ... existing fields ...
    Images []ImageRule `yaml:"images"`
    // Legacy fields kept for backwards-compat. The config-load step
    // synthesizes an Images list from these when Images is empty.
    Image    AzureImage    `yaml:"image"`
    GPUImage AzureGPUImage `yaml:"gpu_image"`
}
```

### Picker function

```go
// PickImage walks rules in order and returns the first image
// whose predicates match the flavor. Returns "" when no rule
// matches — the caller falls back to legacy config / hardcoded
// default. Pure function; no I/O.
func PickImage(rules []ImageRule, flavorName string, hasGPU bool) string
```

## Migration path

This document is design-only. No code lives in the repo for it yet.
A `PickImage` helper + test suite was prototyped at design time and
removed before commit — when Phase 2 starts the helper should be
regenerated from the test plan below, not resurrected from history.
The per-pool `image:` / `gpu_image:` shipped in the current branch
(workflow-level overrides on `worker_pool`) is a separate precedence
tier that stays unchanged across all phases.

Phases:

- **Phase 1** — implement the picker + unit tests (function + test
  plan are specified in this doc).
- **Phase 2** — wire the picker into Azure and OpenStack providers.
  Legacy `image` / `gpu_image` config fields synthesize a 2-entry
  list when `images:` is empty. Deprecation warning when the
  fallback fires.
- **Phase 3** (next release after a deprecation window) — remove the
  legacy field synthesis; the config loader rejects empty `images:`
  on providers that previously used it.

## Test plan (when Phase 1 is implemented)

Pure unit tests on `PickImage`:

- Empty rule list → returns "".
- Default-only rule (no predicates) → returns its image for any flavor.
- First match wins: `[match:Standard_NC%, match:Standard_N%]` resolves
  `Standard_NC24` to the NC rule even though both match.
- `match` LIKE semantics: `%` matches; literal `_` does not.
- `has_gpu` predicate gates the rule; combines with `match` as AND.
- Rule with neither predicate matches universally (the default
  fallback entry).
- Unreachable entries: rules after an unconditional default never
  fire (caller-visible behaviour, not picker-internal — but worth
  asserting so future operators don't expect different).
- Malformed `match` patterns (empty `image:` field, regex-poisonous
  chars in `match`) — picker treats specials as literal; only the
  config validator (out of scope here) rejects empty `image:`.

Phase 2 will layer integration tests on top: per-recruiter override
beats server-config first-match; legacy synthesis preserves today's
behaviour; the deprecation warning fires when expected.

## Open questions

1. **`regex:` predicate as well as `match:`?** Probably not — first-
   match-by-LIKE is enough for naming conventions every provider
   actually has. Add later if a real-world pattern can't be expressed
   with `%` (e.g. "any NC family of CUDA generation ≥ 12").
2. **Per-region rules?** A future `region:` predicate could
   disambiguate per-region image catalogs (OpenStack regions often
   carry different image names for the same logical OS). Mirrors how
   `worker_pool.region` already filters. Out of scope for Phase 1.
