#!/bin/sh
set -eu
exec waydict model install parakeet-v3-int8 "$@"
