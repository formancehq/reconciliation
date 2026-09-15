#!/usr/bin/env bash
set -euo pipefail

[[ "$#" -eq 2 ]] || { printf 'usage: check-coverage.sh COVER_PROFILE MINIMUM_PERCENT\n' >&2; exit 2; }

readonly profile="$1"
readonly minimum="$2"
[[ -f "$profile" ]] || { printf 'coverage profile is missing: %s\n' "$profile" >&2; exit 2; }
[[ "$minimum" =~ ^([0-9]+)(\.[0-9]+)?$ ]] || { printf 'minimum coverage must be a non-negative number: %s\n' "$minimum" >&2; exit 2; }

awk -v minimum="$minimum" '
  NR == 1 {
    if ($0 !~ /^mode: (set|count|atomic)$/) {
      print "malformed coverage mode" > "/dev/stderr"
      exit 2
    }
    next
  }
  NF != 3 || $2 !~ /^[0-9]+$/ || $3 !~ /^[0-9]+$/ {
    print "malformed coverage row at line " NR > "/dev/stderr"
    exit 2
  }
  {
    total += $2
    if ($3 > 0) {
      covered += $2
    }
  }
  END {
    if (NR < 2) {
      print "coverage profile contains no statements" > "/dev/stderr"
      exit 2
    }
    if (total == 0) {
      print "coverage profile contains zero statements" > "/dev/stderr"
      exit 2
    }
    percentage = 100 * covered / total
    if (percentage + 0.0000001 < minimum) {
      printf "application coverage %.1f%% is below minimum %.1f%%\n", percentage, minimum > "/dev/stderr"
      exit 1
    }
    printf "application coverage: %.1f%% (minimum %.1f%%)\n", percentage, minimum
  }
' "$profile"
