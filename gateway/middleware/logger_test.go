package middleware

import (
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRedactQueryRemovesCredentials(t *testing.T) {
	redacted := redactQuery("room=general&token=header.payload.signature&access_token=secret-access&api_key=secret-key")
	values, err := url.ParseQuery(redacted)
	require.NoError(t, err)
	require.Equal(t, "general", values.Get("room"))
	require.Equal(t, "<redacted>", values.Get("token"))
	require.Equal(t, "<redacted>", values.Get("access_token"))
	require.Equal(t, "<redacted>", values.Get("api_key"))
	require.NotContains(t, redacted, "header.payload.signature")
	require.NotContains(t, redacted, "secret-access")
	require.NotContains(t, redacted, "secret-key")
}

func TestRedactQueryFailsClosedForMalformedInput(t *testing.T) {
	require.Equal(t, "<redacted-invalid-query>", redactQuery("token=%zz"))
}
