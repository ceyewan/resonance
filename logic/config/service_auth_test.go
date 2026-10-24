package config

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPilotSigningIdentityMustBeSeparateFromGateway(t *testing.T) {
	c := ServiceAuthConfig{PilotServiceID: "pilot", PilotSecret: strings.Repeat("p", 32), GatewayServiceID: "gateway", GatewaySecret: strings.Repeat("g", 32)}
	require.NoError(t, validatePilotServiceAuth(c, "resonance-agent"))
	c.PilotSecret = c.GatewaySecret
	require.Error(t, validatePilotServiceAuth(c, "resonance-agent"))
}
