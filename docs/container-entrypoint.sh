#!/bin/sh

set -eu

required_legal_variables="
RENART_LEGAL_NAME
RENART_LEGAL_ADDRESS_LINE_1
RENART_LEGAL_POSTAL_CODE
RENART_LEGAL_CITY
RENART_LEGAL_COUNTRY
RENART_LEGAL_EMAIL
RENART_ANALYTICS_RETENTION_DAYS
"

missing_legal_variables=""
for variable_name in $required_legal_variables; do
	variable_value="$(printenv "$variable_name" 2>/dev/null || true)"
	if [ -z "$variable_value" ]; then
		missing_legal_variables="$missing_legal_variables $variable_name"
	fi
done

if [ -n "$missing_legal_variables" ]; then
	printf '%s\n' "Renart docs cannot start: missing required legal environment variables:$missing_legal_variables" >&2
	exit 1
fi

case "$RENART_ANALYTICS_RETENTION_DAYS" in
	*[!0-9]*)
		printf '%s\n' "Renart docs cannot start: RENART_ANALYTICS_RETENTION_DAYS must be a positive integer" >&2
		exit 1
		;;
	0)
		printf '%s\n' "Renart docs cannot start: RENART_ANALYTICS_RETENTION_DAYS must be greater than zero" >&2
		exit 1
		;;
esac

exec "$@"
