import re, subprocess, tempfile, textwrap, difflib
from dataclasses import dataclass
from typing import List, Optional

ALLOWED_BUILTINS = {"std.sh": {"_para","_wait","_strict","_nostrict","_canfail"}, "bio.sh": {"_find_pairs"}}

@dataclass
class Issue:
    level: str   # "error" | "warn" | "info"
    msg: str
    line: Optional[int] = None
    suggestion: Optional[str] = None

IMPORT_RE = re.compile(r'^\s*(?:\.|source)\s+(/builtin/([A-Za-z0-9._-]+))\s*$', re.M)

def _closest(name: str) -> Optional[str]:
    matches = difflib.get_close_matches(name, ALLOWED_BUILTINS.keys(), n=1, cutoff=0.6)
    return matches[0] if matches else None

def validate_shell(cmd: str, shell: str, allow_source_kw: bool = True) -> List[Issue]:
    issues: List[Issue] = []

    # 1) Import guardrail
    for m in IMPORT_RE.finditer(cmd):
        full, fname = m.group(1), m.group(2)
        line_no = cmd[:m.start()].count("\n") + 1

        # enforce leading '.' or 'source' logic
        prefix = cmd[m.start():m.end()].lstrip()
        if not (prefix.startswith(". ") or (allow_source_kw and prefix.startswith("source "))):
            issues.append(Issue("error", f"Invalid import form: {prefix.strip()}", line_no))

        if fname not in ALLOWED_BUILTINS.keys():
            sug = _closest(fname)
            if sug:
                issues.append(Issue(
                    "error",
                    f"Unknown builtin '{fname}' (did you mean '{sug}'?)",
                    line_no,
                    suggestion=f"Replace `{full}` with `/builtin/{sug}`"
                ))
            else:
                issues.append(Issue("error", f"Unknown builtin '{fname}'", line_no))

    # 2) Must import at least one builtin if using helper functions (optional heuristic)
    # Example: if you standardize helper names, list a few to hint import necessity.
    for helper, helper_words in ALLOWED_BUILTINS.items():
        if any(re.search(r'\b' + re.escape(hw) + r'\b', cmd) for hw in helper_words) and not IMPORT_RE.search(cmd):
            issues.append(Issue("error", f"Uses builtin helpers from '{helper}' but no `. /builtin/{helper}` found"))   

    # 3) Parse-only syntax check with bash -n (sanitize builtin imports)
    sanitized = re.sub(IMPORT_RE, ": # /builtin import (ignored for -n)", cmd)
    # ensure a strict prologue so -n parses the same options context users expect
    prologue = "set -euo pipefail\n"
    payload = prologue + sanitized

    try:
        with tempfile.NamedTemporaryFile("w", suffix=".sh", delete=False) as tf:
            tf.write(payload)
            path = tf.name
        cp = subprocess.run(
            [shell, "-n", path],
            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True
        )
        if cp.returncode != 0:
            # bash puts syntax diagnostics on stderr with "line N: ..." — try to extract N
            msg = cp.stderr.strip()
            mline = re.search(r'line\s+(\d+)', msg)
            ln = int(mline.group(1)) - 1 if mline else None  # -1 for the prologue
            issues.append(Issue("error", f"{shell} -n: {msg}", ln))
    except FileNotFoundError:
        issues.append(Issue("warn", f"`{shell}` not found; skipped syntax check"))
    finally:
        try:
            import os; os.unlink(path)
        except Exception:
            pass

    return issues