#!/usr/bin/bash
# Compatibility entry point: the unified Go/Qt build includes the complete audit.
set -euo pipefail
audit_root=$(cd -- "$(dirname -- "$0")/.." && pwd -P)
exec bash "$audit_root/scripts/run_go_migration_build.sh" "$@"
