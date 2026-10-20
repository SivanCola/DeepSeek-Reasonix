#!/usr/bin/env bash
#
# One command from a clean checkout to an installable APK.
#
# The order matters and is the whole content of this script: the kernel and the
# page are inputs to packaging, not build steps inside it, so they are produced
# first and the Gradle task only assembles what is already there. Running
# `gradle assembleRelease` directly on a fresh checkout fails on purpose (see
# `requireBuildInputs` in app/build.gradle) rather than producing an APK with no
# kernel in it.
#
# Usage: ./scripts/build.sh [debug|release]     (default: release)
set -euo pipefail

here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
android="$here/.."
variant="${1:-release}"

case "$variant" in
  debug|release) ;;
  *) echo "usage: $0 [debug|release]" >&2; exit 2 ;;
esac

"$here/build-kernel.sh"
"$here/sync-page.sh"

cd "$android"

# The wrapper is committed, jar and all, and it is the only thing that runs the
# build. Generating one here would tie the dependency versions to whichever
# `gradle` happened to be on PATH, which is the opposite of a pinned build.
if [ ! -f gradlew ]; then
  echo "gradlew is missing: this repository commits the Gradle wrapper, so the checkout is incomplete" >&2
  echo "(a release build does not use `gradle` from PATH: that would tie dependency versions to the machine)" >&2
  exit 1
fi

task="assemble$([ "$variant" = release ] && echo Release || echo Debug)"
echo "packaging the APK ($variant) ..."
./gradlew "$task" --no-daemon

apk_dir="$android/app/build/outputs/apk/$variant"
echo
echo "done. Installable artifacts:"
find "$apk_dir" -name '*.apk' -maxdepth 1 -print -exec ls -lh {} \;
