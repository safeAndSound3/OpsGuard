#!/usr/bin/env sh
set -eu

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
exec sh "$SCRIPT_DIR/agent/deploy/install.sh" "$SCRIPT_DIR/agent/opsguard-agent"
