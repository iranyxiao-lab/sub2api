package main

import (
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBusinessServerDoesNotDependOnSignerServerPackage(t *testing.T) {
	command := exec.Command("go", "list", "-deps", "-f", "{{.ImportPath}}", ".")
	output, err := command.CombinedOutput()
	require.NoError(t, err, string(output))
	for _, dependency := range strings.Fields(string(output)) {
		require.NotEqual(t, "github.com/Wei-Shaw/sub2api/internal/usdtsigner", dependency,
			"the production business process must never embed the signer server")
	}
}
