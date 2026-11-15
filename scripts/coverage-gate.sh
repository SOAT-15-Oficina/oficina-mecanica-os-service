#!/bin/sh
set -eu

PROFILE="${1:-coverage.out}"
MINIMUM="${COVERAGE_MINIMUM:-80}"
EXCLUDE="${COVERAGE_EXCLUDE:-/cmd/|/database/|/docs/|/tests/}"

FILTERED="$(mktemp)"
trap 'rm -f "$FILTERED"' EXIT

head -n 1 "$PROFILE" > "$FILTERED"
tail -n +2 "$PROFILE" | grep -Ev "$EXCLUDE" >> "$FILTERED" || true

TOTAL="$(go tool cover -func="$FILTERED" | awk '/^total:/ { sub("%", "", $3); print $3 }')"
echo "Cobertura: ${TOTAL}% (minimo ${MINIMUM}%)"

if [ -n "${GITHUB_STEP_SUMMARY:-}" ]; then
  echo "### Cobertura: ${TOTAL}% (minimo ${MINIMUM}%)" >> "$GITHUB_STEP_SUMMARY"
fi

awk -v total="$TOTAL" -v minimum="$MINIMUM" 'BEGIN { exit (total + 0 >= minimum + 0) ? 0 : 1 }'
