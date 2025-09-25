package client

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
)

const helperFolder = "builtin"

const stdSh = `
_strict() {
  set -e
  set -o pipefail 2>/dev/null || true
}

_nostrict() {
  set +e
  set +o pipefail 2>/dev/null || true
}

_canfail() {
  if [ "$#" -gt 0 ]; then
    # save current state (best-effort)
    _had_errexit=0; (set -o | grep -q 'errexit.*on') && _had_errexit=1
    _had_pipefail=0; (set -o 2>/dev/null | grep -q 'pipefail.*on') && _had_pipefail=1

    set +e
    set +o pipefail 2>/dev/null || true
    "$@"; _rc=$?

    # restore
    [ "$_had_errexit" -eq 1 ] && set -e || set +e
    if [ "$_had_pipefail" -eq 1 ]; then
      set -o pipefail 2>/dev/null || true
    else
      set +o pipefail 2>/dev/null || true
    fi
    return $_rc
  else
    _nostrict
  fi
}

_para() {
  _scitq_tab=$(echo -e "\t")
  _scitq_nl=$(echo -e "\n")
  _scitq_cmd=$(echo "$*"|sed -e 's/ /🧬/g' -e "s/$_scitq_tab/🦀/g" -e "s/$_scitq_nl/💥/g")
  "$@" &
  _scitq_pid=$!
  _SCITQ_CMDS="${_SCITQ_CMDS} ${_scitq_cmd}🔥$_scitq_pid"
}

_wait() {
  _scitq_tmp_cmds=$_SCITQ_CMDS
  _scitq_running_cmds=""
  unset _SCITQ_CMDS
  for _scitq_cmdpid in $(echo $_scitq_tmp_cmds); do
    _scitq_pid=$(echo $_scitq_cmdpid|sed 's/.*🔥//')
    if kill -0 $_scitq_pid 2>/dev/null; then
      _scitq_running_cmds="$_scitq_running_cmds $_scitq_cmdpid"
    else
      wait $_scitq_pid
      _scitq_code=$?
      if [ "$_scitq_code" -ne 0 ]; then
        _scitq_cmd=$(echo $_scitq_cmdpid|sed -e 's/🔥.*//' -e "s/💥/$_scitq_nl/g" -e "s/🦀/$_scitq_tab/g" -e 's/🧬/ /g')
        echo "Command failed (PID $_scitq_pid): $_scitq_cmd" >&2
        return "$_scitq_code"
      fi
    fi
  done
  for _scitq_cmdpid in $(echo $_scitq_running_cmds); do
    _scitq_pid=$(echo $_scitq_cmdpid|sed 's/.*🔥//')
    wait $_scitq_pid
    _scitq_code=$?
    if [ "$_scitq_code" -ne 0 ]; then
      _scitq_cmd=$(echo $_scitq_cmdpid|sed -e 's/🔥.*//' -e "s/💥/$_scitq_nl/g" -e "s/🦀/$_scitq_tab/g" -e 's/🧬/ /g')
      echo "Command failed (PID $_scitq_pid): $_scitq_cmd" >&2
      return "$_scitq_code"
    fi
  done
  return 0
}

_retry() {
  _max="$1"
  _delay="$2"
  case "$_max" in
    ''|*[!0-9]*) _max=5; _delay=1 ;;
    *)
      case "$_delay" in
        ''|*[!0-9]*) _delay=1; shift 1 ;;
        *) shift 2 ;;
      esac
      ;;
  esac
  _n=0
  until "$@"; do
    _n=$(( _n + 1 ))
    if [ "$_n" -ge "$_max" ]; then
      echo "❌ Retry failed after $_n attempts: $*" >&2
      return 1
    fi
    sleep "$_delay"
  done
}

_strict`

const bioSh = `
_find_pairs() {
  # do not assume arrays; keep it POSIX
  for sep in _ . r R ""; do
    r1=""; r2=""; extra=""
    c1=0;  c2=0
    for fq do
      case "$fq" in
        *${sep}1.f*q.gz) r1="${r1}${r1:+ }$fq"; c1=$((c1+1));;
        *${sep}2.f*q.gz) r2="${r2}${r2:+ }$fq"; c2=$((c2+1));;
        *)                extra="${extra}${extra:+ }$fq";;
      esac
    done
    if [ "$c1" -gt 0 ] && [ "$c1" -eq "$c2" ]; then
      READS1=$r1
      READS2=$r2
      EXTRA=$extra
      return 0
    fi
    # else: try next separator
  done
  return 1
}

`

func createHelpers(store string) {
	dir := filepath.Join(store, helperFolder)
	if err := os.MkdirAll(dir, 0o777); err != nil {
		log.Printf("failed to create %s: %v", dir, err)
		return
	}

	if err := writeAtomic(filepath.Join(dir, "std.sh"), []byte(stdSh+"\n"), 0o644); err != nil {
		log.Printf("failed to write std.sh: %v", err)
	}
	if err := writeAtomic(filepath.Join(dir, "bio.sh"), []byte(bioSh+"\n"), 0o644); err != nil {
		log.Printf("failed to write bio.sh: %v", err)
	}
}

func writeAtomic(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp := filepath.Join(dir, "."+base+".tmp")

	// Write to a temp file first
	if err := os.WriteFile(tmp, data, perm); err != nil {
		return fmt.Errorf("write temp: %w", err)
	}
	// fsync by reopen and Sync to be extra safe (best-effort)
	if f, err := os.OpenFile(tmp, os.O_RDWR, perm); err == nil {
		_ = f.Sync()
		_ = f.Close()
	}
	// Rename atomically
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename: %w", err)
	}
	// Ensure final perms
	if err := os.Chmod(path, perm); err != nil {
		return fmt.Errorf("chmod: %w", err)
	}
	return nil
}
