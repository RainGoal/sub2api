package service

import (
	"os"
	"os/exec"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// runAllocationTestInSubprocess returns true after running this test in a fresh
// process. AllocsPerRun counts process-wide allocations, so background work left
// by other service tests must be excluded from allocation measurements.
func runAllocationTestInSubprocess(t *testing.T) bool {
	t.Helper()
	const isolatedEnv = "SUB2API_TEST_ISOLATED_ALLOCS"
	if os.Getenv(isolatedEnv) == t.Name() {
		return false
	}
	binary, err := os.Executable()
	require.NoError(t, err)
	command := exec.Command(binary, "-test.run=^"+regexp.QuoteMeta(t.Name())+"$", "-test.count=1", "-test.timeout=30s")
	command.Env = append(os.Environ(), isolatedEnv+"="+t.Name())
	output, err := command.CombinedOutput()
	require.NoError(t, err, "isolated allocation check failed:\n%s", output)
	return true
}
