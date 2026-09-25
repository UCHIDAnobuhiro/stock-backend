#!/usr/bin/env bash
set -euo pipefail

if (( $# < 2 || $# > 3 )); then
  echo "usage: $0 <build-tag> <test-prefix> [test-directory]" >&2
  exit 2
fi

tag="$1"
prefix="$2"
test_dir="${3:-internal}"
if [[ ! "${tag}" =~ ^[[:alpha:]][[:alnum:]_]*$ || ! "${prefix}" =~ ^Test[[:alnum:]_]*$ || ! -d "${test_dir}" ]]; then
  echo "invalid build tag, test prefix, or test directory" >&2
  exit 2
fi

files="$(find "${test_dir}" -type f -name '*_test.go' -exec grep -l "^//go:build ${tag}$" {} + | sort || true)"
if [[ -z "${files}" ]]; then
  echo "::error::no ${tag}-tagged test files found"
  exit 1
fi

invalid=0
found=0
while IFS= read -r file; do
  if grep -qE "^func ${prefix}[[:alnum:]_]*\\(" "${file}"; then
    found=1
  fi
  if output="$(awk -v prefix="${prefix}" '
    BEGIN { expected = "^func " prefix "[[:alnum:]_]*\\(" }
    /^func Test[[:alnum:]_]*\(/ && !/^func TestMain\(/ && $0 !~ expected {
      print FNR ":" $0
      invalid = 1
    }
    END { exit invalid }
  ' "${file}")"; then
    :
  else
    echo "${output}"
    echo "::error file=${file}::${tag} tests must use the ${prefix} prefix"
    invalid=1
  fi
done <<< "${files}"

if (( !found )); then
  echo "::error::no ${prefix} tests found in ${tag}-tagged files"
  invalid=1
fi
if (( invalid )); then
  exit 1
fi

echo "All ${tag}-tagged tests use the ${prefix} prefix."
