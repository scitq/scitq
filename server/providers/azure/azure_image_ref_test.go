package azure

import "testing"

func TestParseAzureImageRef_FourParts(t *testing.T) {
	ref, err := parseAzureImageRef("microsoft-dsvm/ubuntu-hpc/2204/latest")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *ref.Publisher != "microsoft-dsvm" || *ref.Offer != "ubuntu-hpc" || *ref.SKU != "2204" || *ref.Version != "latest" {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestParseAzureImageRef_ThreePartsDefaultsToLatest(t *testing.T) {
	// `microsoft-dsvm/ubuntu-hpc/2204` (no version) is a common
	// shorthand: the operator pins SKU but lets Azure pick the
	// newest version. The parser must default Version="latest".
	ref, err := parseAzureImageRef("microsoft-dsvm/ubuntu-hpc/2204")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *ref.Version != "latest" {
		t.Fatalf("expected Version=latest, got %q", *ref.Version)
	}
	if *ref.SKU != "2204" {
		t.Fatalf("expected SKU=2204, got %q", *ref.SKU)
	}
}

func TestParseAzureImageRef_RejectsMalformed(t *testing.T) {
	cases := []string{
		"",
		"single",
		"two/parts",
		"five/parts/in/this/path",
		"empty///field",
		"  /offer/sku/version",
	}
	for _, s := range cases {
		if _, err := parseAzureImageRef(s); err == nil {
			t.Errorf("expected error parsing %q, got none", s)
		}
	}
}
