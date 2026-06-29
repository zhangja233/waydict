#!/bin/sh
set -eu
exec sway-voice model install parakeet-v3-int8 "$@"
