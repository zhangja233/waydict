#!/usr/bin/env sh
set -eu
exec waydict model install ggml-large-v3-turbo "$@"
