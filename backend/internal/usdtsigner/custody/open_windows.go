//go:build windows

package custody

import (
	"fmt"
	"strings"

	"github.com/google/go-tpm/tpm2/transport"
	"github.com/google/go-tpm/tpm2/transport/windowstpm"
)

func OpenTPM(device string) (transport.TPMCloser, error) {
	device = strings.TrimSpace(device)
	if device != "" && !strings.EqualFold(device, "windows-tbs") {
		return nil, fmt.Errorf("Windows TPM transport must use windows-tbs")
	}
	return windowstpm.Open()
}
