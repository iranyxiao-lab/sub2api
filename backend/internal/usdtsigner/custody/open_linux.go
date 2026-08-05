//go:build linux

package custody

import (
	"fmt"
	"strings"

	"github.com/google/go-tpm/tpm2/transport"
	"github.com/google/go-tpm/tpm2/transport/linuxtpm"
)

func OpenTPM(device string) (transport.TPMCloser, error) {
	device = strings.TrimSpace(device)
	if device == "" {
		return nil, fmt.Errorf("TPM device path is required")
	}
	return linuxtpm.Open(device)
}
