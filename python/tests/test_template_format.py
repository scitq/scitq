"""A template written for a format this scitq does not know is rejected,
rather than run with a possibly different meaning."""
import pytest

from scitq2.yaml_runner import SUPPORTED_FORMATS, run_yaml


def test_unknown_format_is_rejected(capsys):
    newer = max(SUPPORTED_FORMATS) + 1
    template = {"format": newer, "name": "t", "steps": [{"name": "s", "command": "true"}]}
    with pytest.raises(SystemExit):
        run_yaml(template)
    err = capsys.readouterr().err
    assert f"format {newer} is not supported" in err
    assert "Upgrade the scitq server" in err
