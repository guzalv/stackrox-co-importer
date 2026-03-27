#!/usr/bin/env bash
# Check that every IMP-* requirement ID from spec files appears in at least
# one test file (step definitions or Go tests).
#
# USAGE: ./hack/check-spec-coverage.sh
# EXIT:  0 if all IDs covered, 1 if any are missing

set -euo pipefail

SPECS_DIR="$(cd "$(dirname "$0")/../specs" && pwd)"
PROJECT_ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Extract all IMP-* IDs from spec files (unique, sorted)
spec_ids=$(grep -rhoP 'IMP-[A-Z]+-\d+[a-z]?' "$SPECS_DIR" | sort -u)

missing=0
for id in $spec_ids; do
  # Search in all _test.go files (features/ + internal/)
  if ! grep -rq "$id" "$PROJECT_ROOT/features/" "$PROJECT_ROOT/internal/" "$PROJECT_ROOT/e2e/" 2>/dev/null; then
    echo "MISSING: $id — not found in any test file"
    missing=$((missing + 1))
  fi
done

total=$(echo "$spec_ids" | wc -l | tr -d ' ')
covered=$((total - missing))

echo ""
echo "Spec coverage: ${covered}/${total} requirement IDs covered"

if [ "$missing" -gt 0 ]; then
  echo "FAIL: $missing requirement IDs missing from tests"
  exit 1
fi

echo "PASS: all requirement IDs found in tests"
