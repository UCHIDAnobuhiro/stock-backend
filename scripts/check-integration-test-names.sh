#!/usr/bin/env bash
set -euo pipefail

files="$(find internal -type f -name '*_test.go' -exec grep -l '^//go:build integration$' {} + | sort || true)"
if [[ -z "${files}" ]]; then
  echo "::error::no integration-tagged test files found"
  exit 1
fi

invalid=0
while IFS= read -r file; do
  if output="$(awk '/^func Test[[:alnum:]_]*\(/ && !/^func TestMain\(/ && !/^func TestIntegration[[:alnum:]_]*\(/ { print FNR ":" $0; invalid = 1 } END { exit invalid }' "${file}")"; then
    :
  else
    echo "${output}"
    echo "::error file=${file}::integration tests must use the TestIntegration prefix"
    invalid=1
  fi
done <<< "${files}"

if ((invalid)); then
  exit 1
fi

echo "All integration-tagged tests use the TestIntegration prefix."
