package cmd

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"

	webhttpapi "renart/internal/web/httpapi"
)

const (
	accessTokenEnv       = "RENART_ACCESS_TOKEN"
	minAccessTokenLength = 16
)

// accessToken is the shared secret `renart web` requires on every request
// when access auth is on. generated marks a per-process token the operator
// has to be shown once.
type accessToken struct {
	value     string
	generated bool
}

// resolveWebAccessToken decides whether `renart web` turns access auth on:
// always when RENART_ACCESS_TOKEN is set (loopback behind a reverse proxy),
// and with a generated per-process token when the bind host is non-loopback.
// Plain loopback without the variable keeps access auth off.
func resolveWebAccessToken(host string, getenv func(string) string, generate func() string) (accessToken, error) {
	if value := strings.TrimSpace(getenv(accessTokenEnv)); value != "" {
		if len(value) < minAccessTokenLength {
			return accessToken{}, fmt.Errorf("%s must be at least %d characters; use a long random value, for example the output of `openssl rand -hex 32`", accessTokenEnv, minAccessTokenLength)
		}
		return accessToken{value: value}, nil
	}
	if isLoopbackBindHost(host) {
		return accessToken{}, nil
	}
	value := generate()
	if value == "" {
		return accessToken{}, errors.New("failed to generate an access token; set " + accessTokenEnv + " instead")
	}
	return accessToken{value: value, generated: true}, nil
}

// webAccessOptions builds the guard options for `renart web`. The cookie
// name carries the listener port so two servers on one host do not
// overwrite each other's sign-in.
func webAccessOptions(sessionToken string, token accessToken, address string) webhttpapi.AccessOptions {
	opts := webhttpapi.AccessOptions{SessionToken: sessionToken, AccessToken: token.value}
	if token.value != "" {
		if _, port, err := net.SplitHostPort(address); err == nil && port != "" {
			opts.CookieName = "renart_access_" + port
		}
	}
	return opts
}

// standaloneAccessOptions builds the guard options for the desktop shell,
// which only ever listens on loopback for its own webview: access auth stays
// off even when RENART_ACCESS_TOKEN is set.
func standaloneAccessOptions(sessionToken string) webhttpapi.AccessOptions {
	return webhttpapi.AccessOptions{SessionToken: sessionToken}
}

// printAccessTokenNotice tells the operator how to sign in. A generated
// token is printed here once and nowhere else (never to the log).
func printAccessTokenNotice(out io.Writer, appURL string, token accessToken) {
	if token.value == "" {
		return
	}
	if token.generated {
		_, _ = fmt.Fprintf(out, "Access token: %s\n", token.value)
		_, _ = fmt.Fprintf(out, "Set %s to keep the access token the same across restarts.\n", accessTokenEnv)
	} else {
		_, _ = fmt.Fprintf(out, "Access token: taken from %s.\n", accessTokenEnv)
	}
	_, _ = fmt.Fprintf(out, "Sign in at %s/auth, or send the token as \"Authorization: Bearer <token>\".\n", appURL)
}
