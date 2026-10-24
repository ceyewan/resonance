package pi

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPiProcessHasNoToolsOrHostSecrets(t *testing.T) {
	cfg := testAdapterConfig(t, Config{})
	req := testRunRequest(t, "run-1")
	spec, err := cfg.buildProcessSpec(req)
	require.NoError(t, err)
	require.Contains(t, spec.Args, "--no-builtin-tools")
	require.Contains(t, spec.Args, "--no-extensions")
	require.Contains(t, spec.Args, "--no-context-files")
	require.Contains(t, spec.Args, "--no-skills")
	require.NotContains(t, spec.Env, "RESONANCE_POSTGRES_PASSWORD")
	req.MaxOutputTokens = 0
	_, err = cfg.buildProcessSpec(req)
	require.Error(t, err)
}
