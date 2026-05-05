package updater

import "testing"

func TestFlavorFilter_DefaultsAllowEverything(t *testing.T) {
	f, err := CompileFlavorFilter(nil, nil)
	if err != nil {
		t.Fatalf("CompileFlavorFilter: %v", err)
	}
	for _, name := range []string{
		"Standard_D32s_v5",
		"Standard_DC32ads_cc_v5",
		"r3-64",
		"",
	} {
		if !f.Allows(name) {
			t.Errorf("default filter must allow %q", name)
		}
	}
}

func TestFlavorFilter_NilReceiverAllowsAll(t *testing.T) {
	var f *FlavorFilter
	if !f.Allows("anything") {
		t.Errorf("nil *FlavorFilter must allow everything (so call sites don't need a nil guard)")
	}
}

func TestFlavorFilter_ExcludeOnly(t *testing.T) {
	f, err := CompileFlavorFilter(nil, []string{`_cc_v\d+$`, `-\d+as_v\d+$`})
	if err != nil {
		t.Fatalf("CompileFlavorFilter: %v", err)
	}
	cases := []struct {
		name string
		want bool
	}{
		// Confidential VMs blocked by first pattern.
		{"Standard_DC32ads_cc_v5", false},
		{"Standard_EC32as_cc_v5", false},
		// Constrained-core VMs blocked by second pattern.
		{"Standard_E32-16as_v4", false},
		{"Standard_E64-32as_v5", false},
		// Healthy E-series allowed.
		{"Standard_E32bds_v5", true},
		{"Standard_E32s_v5", true},
		{"Standard_D32ads_v5", true},
		{"r3-64", true},
	}
	for _, c := range cases {
		got := f.Allows(c.name)
		if got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFlavorFilter_IncludeOnly(t *testing.T) {
	// Operator says: only modern E and D v5 series.
	f, err := CompileFlavorFilter([]string{`^Standard_E.*_v5$`, `^Standard_D.*_v5$`}, nil)
	if err != nil {
		t.Fatalf("CompileFlavorFilter: %v", err)
	}
	cases := []struct {
		name string
		want bool
	}{
		{"Standard_E32bds_v5", true},
		{"Standard_D32ads_v5", true},
		// Older versions filtered out.
		{"Standard_E32_v3", false},
		{"Standard_D32_v4", false},
		// Different family filtered out.
		{"Standard_F32s_v2", false},
		{"r3-64", false},
	}
	for _, c := range cases {
		got := f.Allows(c.name)
		if got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFlavorFilter_IncludeAndExclude(t *testing.T) {
	// Operator says: only modern E/D v5 series, but NOT confidential
	// (`_cc_`) and NOT constrained-core (`-Nas`).
	f, err := CompileFlavorFilter(
		[]string{`^Standard_E.*_v5$`, `^Standard_D.*_v5$`},
		[]string{`_cc_v\d+$`, `-\d+as_v\d+$`},
	)
	if err != nil {
		t.Fatalf("CompileFlavorFilter: %v", err)
	}
	cases := []struct {
		name string
		want bool
	}{
		// Allowed: matches include AND not in exclude.
		{"Standard_E32bds_v5", true},
		{"Standard_E64bds_v5", true},
		{"Standard_D32ads_v5", true},
		// Excluded: matches include but ALSO matches exclude.
		{"Standard_E32-16as_v5", false},
		{"Standard_DC32ads_cc_v5", false},
		// Filtered by include alone: doesn't match include patterns.
		{"Standard_E32_v3", false},
		{"r3-64", false},
	}
	for _, c := range cases {
		got := f.Allows(c.name)
		if got != c.want {
			t.Errorf("Allows(%q) = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestFlavorFilter_BadPattern_Errors(t *testing.T) {
	_, err := CompileFlavorFilter([]string{`[`}, nil)
	if err == nil {
		t.Fatalf("expected error for bad include pattern")
	}
	if !contains(err.Error(), "flavor_include_patterns[0]") {
		t.Errorf("error should identify the failing pattern's list and index, got: %v", err)
	}

	_, err = CompileFlavorFilter(nil, []string{`good`, `(unclosed`})
	if err == nil {
		t.Fatalf("expected error for bad exclude pattern")
	}
	if !contains(err.Error(), "flavor_exclude_patterns[1]") {
		t.Errorf("error should identify the failing pattern's list and index, got: %v", err)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
