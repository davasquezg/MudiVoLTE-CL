#!/bin/sh
# Tests of the web pages that run outside the router: the GL admin panel page
# with Vue 2 in node, and the LuCI page with LuCI's modules as stubs. Vue goes
# into a temporary directory, not the project.
#   package/tests/run.sh
set -eu
here=$(CDPATH='' cd -- "$(dirname -- "$0")" && pwd)
deps=$(mktemp -d)
trap 'rm -rf "$deps"' EXIT
# npm installs into the nearest directory with a package.json above the
# current one (on Windows that can be the home directory): the temporary
# directory gets its own, and --prefix names it too.
echo '{}' >"$deps/package.json"
(cd "$deps" && npm install --prefix "$deps" --no-audit --no-fund --silent \
	vue@2.7.16 vue-server-renderer@2.7.16 >/dev/null)
# node on Windows (Git Bash, MSYS2) wants a Windows path in NODE_PATH.
modules=$deps/node_modules
if command -v cygpath >/dev/null 2>&1; then
	modules=$(cygpath -w "$modules")
fi
NODE_PATH=$modules node "$here/gl-view.test.js"
node "$here/luci-view.test.js"
