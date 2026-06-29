"""Tests for the `image:` / `gpu_image:` worker-pool overrides.

Both are per-pool cloud image strings forwarded to the recruiter
row and consumed by the provider Create() with precedence
`gpu_image (when has_gpu) > image > scitq.yaml default`. These
tests cover the Python contract: kwarg → extra_options → recruiter
options → grpc payload, and the YAML key → WorkerPool kwarg path.
"""
import pytest

from scitq2.recruit import WorkerPool, W
from scitq2.workflow import TaskSpec
from scitq2.yaml_runner import _build_worker_pool


def test_image_kwarg_lands_in_extra_options():
    pool = WorkerPool(W.cpu >= 8, image="Canonical/UbuntuServer/24.04-LTS/latest")
    assert pool.extra_options.get("image") == "Canonical/UbuntuServer/24.04-LTS/latest"
    assert "gpu_image" not in pool.extra_options


def test_gpu_image_kwarg_lands_in_extra_options():
    pool = WorkerPool(W.has_gpu == True,
                      gpu_image="microsoft-dsvm/ubuntu-hpc/2204/latest")  # noqa: E712
    assert pool.extra_options.get("gpu_image") == "microsoft-dsvm/ubuntu-hpc/2204/latest"
    assert "image" not in pool.extra_options


def test_both_images_lands_in_extra_options():
    pool = WorkerPool(W.cpu >= 8,
                      image="my-baked-image",
                      gpu_image="my-gpu-baked-image")
    assert pool.extra_options.get("image") == "my-baked-image"
    assert pool.extra_options.get("gpu_image") == "my-gpu-baked-image"


def test_no_image_keys_when_unset():
    pool = WorkerPool(W.cpu >= 8)
    assert "image" not in pool.extra_options
    assert "gpu_image" not in pool.extra_options


def test_build_recruiter_forwards_image_strings():
    # The recruiter options dict is what flows into create_recruiter
    # over gRPC; both fields must come through verbatim.
    pool = WorkerPool(W.cpu >= 8,
                      image="my-img",
                      gpu_image="my-gpu-img")
    ts = TaskSpec(cpu=4)
    options = pool.build_recruiter(ts)
    assert options["image"] == "my-img"
    assert options["gpu_image"] == "my-gpu-img"


def test_clone_with_preserves_images_by_default():
    pool = WorkerPool(W.cpu >= 8, image="img-A", gpu_image="gpu-A")
    cloned = pool.clone_with(max_recruited=4)
    assert cloned.extra_options.get("image") == "img-A"
    assert cloned.extra_options.get("gpu_image") == "gpu-A"


def test_clone_with_overrides_images():
    pool = WorkerPool(W.cpu >= 8, image="img-A", gpu_image="gpu-A")
    cloned = pool.clone_with(image="img-B", gpu_image="gpu-B")
    assert cloned.extra_options.get("image") == "img-B"
    assert cloned.extra_options.get("gpu_image") == "gpu-B"


# ---------------- YAML wiring ----------------


def test_yaml_image_key_lands_on_pool():
    wp_def = {
        'cpu': '>= 8',
        'image': 'Canonical/UbuntuServer/24.04-LTS/latest',
        'gpu_image': 'microsoft-dsvm/ubuntu-hpc/2204/latest',
    }
    pool = _build_worker_pool(wp_def, params=None, extra_vars=None)
    assert pool.extra_options.get('image') == 'Canonical/UbuntuServer/24.04-LTS/latest'
    assert pool.extra_options.get('gpu_image') == 'microsoft-dsvm/ubuntu-hpc/2204/latest'


def test_yaml_empty_image_string_is_ignored():
    # YAML interpolation can produce empty strings (e.g. an unset
    # template default). Treat them as "not specified" rather than
    # forwarding an empty string to the recruiter — the provider
    # would otherwise try to parse the empty string and fall through
    # anyway, but we'd rather not store noise.
    wp_def = {'cpu': '>= 8', 'image': '', 'gpu_image': ''}
    pool = _build_worker_pool(wp_def, params=None, extra_vars=None)
    assert 'image' not in pool.extra_options
    assert 'gpu_image' not in pool.extra_options
