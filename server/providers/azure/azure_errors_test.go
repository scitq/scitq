package azure

import (
	"fmt"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

func TestUnsupportedFlavorErrors(t *testing.T) {
	// The SDK puts Azure's message in the error text; the wrapping
	// error carries it here.
	azErr := func(status int, code, message string) error {
		return fmt.Errorf("PUT …/virtualMachines/w1: %w: %s", &azcore.ResponseError{StatusCode: status, ErrorCode: code}, message)
	}
	for _, c := range []struct {
		status      int
		code, msg   string
		unsupported bool
	}{
		{400, "InvalidParameter", "The requested VM size Standard_EC8ads_cc_v5 is not available in the current region. The sizes available in the current region are: Standard_D2a_v4,…", true},
		{400, "BadRequest", "The VM size Standard_DC4ads_cc_v5 is not supported for creation of VMs with security type Standard", true},
		{400, "InvalidParameter", "Required parameter 'adminPassword' is missing (null).", false},
		{409, "SkuNotAvailable", "The requested VM size is currently not available in location 'northeurope'", false},
	} {
		if got := isUnsupportedFlavorError(azErr(c.status, c.code, c.msg)); got != c.unsupported {
			t.Errorf("%d %s %q: unsupported=%v, want %v", c.status, c.code, c.msg, got, c.unsupported)
		}
	}
}
