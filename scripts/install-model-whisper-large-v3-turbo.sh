#!/usr/bin/env sh
set -eu
exec waydict model install whisper-large-v3-turbo "$@"
