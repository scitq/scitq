package server

import "testing"

// parseHistoryFieldMask is a small pure function; unit test it directly
// so behaviour is locked in without needing the full integration harness.

func TestFieldMask_EmptyIsAllTrue(t *testing.T) {
	m := parseHistoryFieldMask(nil)
	if !(m.cpu && m.mem && m.iowait && m.peakCPU && m.peakMem && m.peakIowait && m.peakDisk && m.effConc && m.running && m.lastThrottle) {
		t.Fatalf("nil selector should return all-true, got %+v", m)
	}
}

func TestFieldMask_SelectsOnlyRequested(t *testing.T) {
	m := parseHistoryFieldMask([]string{"peak_mem"})
	if !m.peakMem {
		t.Errorf("peak_mem should be selected")
	}
	// Nothing else should be on (except step_id which is force-on so
	// selective plots still know which step a sample belonged to).
	for name, on := range map[string]bool{
		"cpu": m.cpu, "mem": m.mem, "iowait": m.iowait,
		"peak_cpu": m.peakCPU, "peak_iowait": m.peakIowait, "peak_disk": m.peakDisk,
		"effective_concurrency": m.effConc, "running_tasks": m.running,
		"last_throttle_at": m.lastThrottle,
	} {
		if on {
			t.Errorf("%s should not be selected", name)
		}
	}
}

func TestFieldMask_AcceptsShortAndLongNames(t *testing.T) {
	// Both "peak_mem" and "peak_mem_percent" refer to the same field —
	// no reason to force the caller to spell it one way.
	for _, alias := range []string{"peak_mem", "peak_mem_percent"} {
		m := parseHistoryFieldMask([]string{alias})
		if !m.peakMem {
			t.Errorf("alias %q should map to peak_mem", alias)
		}
	}
}

func TestFieldMask_UnknownNamesIgnored(t *testing.T) {
	// A typo yields fewer fields, not an error. Also: step_id is still
	// force-on even when the caller only asks for garbage.
	m := parseHistoryFieldMask([]string{"peak_mem", "nonexistent_field", "cpu_percentt"})
	if !m.peakMem {
		t.Errorf("known name should still be selected")
	}
	if m.cpu {
		t.Errorf("typo 'cpu_percentt' must not enable cpu")
	}
	if !m.step {
		t.Errorf("step should always be force-on")
	}
}

func TestFieldMask_StepAlwaysOnWhenSelectorNonEmpty(t *testing.T) {
	// Even if the caller doesn't ask for step, we force it on so the
	// per-sample step attribution remains available for any workflow-
	// scoped plot.
	m := parseHistoryFieldMask([]string{"peak_iowait"})
	if !m.step {
		t.Errorf("step should be force-on when any selector is given")
	}
}
