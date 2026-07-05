#!/usr/bin/env sh
set -eu
exec waydict model install parakeet-unified-en-0.6b-fp32 "$@"
