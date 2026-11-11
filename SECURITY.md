# Security policy

Renart is in public alpha. Security fixes are applied to the latest alpha
release; older alpha versions are not supported once a newer release is
available.

## Report a vulnerability

Please do not open a public issue for a suspected vulnerability. Use
[GitHub's private vulnerability reporting](https://github.com/renart-data/renart/security/advisories/new)
and include the affected version, impact, reproduction steps, and any proposed
mitigation. You should receive an acknowledgement within seven days.

## Local trust boundary

Renart reads and writes the current Git workspace and can execute
user-authored pipeline code. The web server listens on loopback by default.
A non-loopback bind requires the explicit `--unsafe-allow-remote` flag, and
then every request needs an access token: the value of `RENART_ACCESS_TOKEN`,
or a token generated and printed at startup. Setting `RENART_ACCESS_TOKEN` also
turns the check on for a loopback bind behind a reverse proxy. Use a long
random value (for example `openssl rand -hex 32`) and TLS, either
`--tls-cert`/`--tls-key` or a TLS-terminating proxy; without TLS the token and
all traffic cross the network unencrypted. The token is one shared secret, not
user accounts or roles: anyone who holds it can edit files and run code.

Treat workspaces, pipeline code, dependencies, connection definitions, and
notebooks as executable input. Review unknown repositories before opening or
running them, and use least-privilege warehouse credentials.
