//go:build !linux && !windows

package custody

import (
	"fmt"

	"github.com/google/go-tpm/tpm2/transport"
)

func OpenTPM(string) (transport.TPMCloser, error) {
	return nil, fmt.Errorf("physical TPM transport is unsupported on this platform")
}
