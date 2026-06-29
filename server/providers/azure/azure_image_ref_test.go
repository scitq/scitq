package azure

import (
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/compute/armcompute"
)

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

// planForImageRef: commercial publishers (NVIDIA, third-parties)
// MUST receive a Plan block on the VM-create payload —
// VMMarketplaceInvalidInput otherwise. Plan publisher/product/name
// equal the image publisher/offer/sku verbatim. alpha2 incident
// 2026-06-29 worker 6127 on Standard_NV6ads_A10_v5 with image
// nvidia/nvidia-vws-ubuntu-24-lts/ubuntu24lts_with_vgpu18-0/20.0.0
// — Azure rejected the VM with "Creating a virtual machine from
// Marketplace image ... requires Plan information in the request".
func TestPlanForImageRef_NvidiaMarketplaceGetsPlan(t *testing.T) {
	ref := &armcompute.ImageReference{
		Publisher: to.Ptr("nvidia"),
		Offer:     to.Ptr("nvidia-vws-ubuntu-24-lts"),
		SKU:       to.Ptr("ubuntu24lts_with_vgpu18-0"),
		Version:   to.Ptr("20.0.0"),
	}
	plan := planForImageRef(ref)
	if plan == nil {
		t.Fatal("expected Plan block for nvidia publisher, got nil")
	}
	if *plan.Publisher != "nvidia" || *plan.Product != "nvidia-vws-ubuntu-24-lts" || *plan.Name != "ubuntu24lts_with_vgpu18-0" {
		t.Fatalf("unexpected plan: %+v", plan)
	}
}

// First-party publishers must NOT receive a Plan: setting one
// produces "InvalidParameter: The Plan information is invalid".
// This covers the existing NC happy path (microsoft-dsvm) so the
// fix doesn't regress today's working deployments.
func TestPlanForImageRef_FirstPartyPublishersSkipped(t *testing.T) {
	cases := []string{
		"Canonical",
		"microsoft-dsvm",
		"MicrosoftWindowsServer",
		"MicrosoftCblMariner",
		"RedHat",
		"OpenLogic",
		"Debian",
		"SUSE",
	}
	for _, publisher := range cases {
		ref := &armcompute.ImageReference{
			Publisher: to.Ptr(publisher),
			Offer:     to.Ptr("any-offer"),
			SKU:       to.Ptr("any-sku"),
			Version:   to.Ptr("latest"),
		}
		if plan := planForImageRef(ref); plan != nil {
			t.Errorf("publisher=%q must NOT get a Plan block, got %+v", publisher, plan)
		}
	}
}

// Nil and partially-nil refs are tolerated (defensive — the
// real call path always populates Publisher, but the picker
// returning ImageReference from a malformed config could
// theoretically land here).
func TestPlanForImageRef_HandlesNil(t *testing.T) {
	if plan := planForImageRef(nil); plan != nil {
		t.Errorf("nil ImageReference must return nil Plan")
	}
	if plan := planForImageRef(&armcompute.ImageReference{}); plan != nil {
		t.Errorf("ImageReference with nil Publisher must return nil Plan")
	}
}

// Unknown publisher (not in the free allowlist) is treated as
// commercial — get a Plan block. Conservative default: when in
// doubt, send Plan and let Azure error if it's actually wrong,
// rather than silently fail with VMMarketplaceInvalidInput.
func TestPlanForImageRef_UnknownPublisherDefaultsToCommercial(t *testing.T) {
	ref := &armcompute.ImageReference{
		Publisher: to.Ptr("some-third-party-vendor"),
		Offer:     to.Ptr("some-offer"),
		SKU:       to.Ptr("some-sku"),
		Version:   to.Ptr("latest"),
	}
	if plan := planForImageRef(ref); plan == nil {
		t.Error("unknown publisher should default to commercial (Plan set)")
	}
}
