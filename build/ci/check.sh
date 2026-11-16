#!/bin/sh
# Runs the Linux checks. Usage: build/ci/check.sh [go|frontend|audio]...
# With no argument it runs go and frontend. Meant to run inside
# build/ci/Dockerfile.
#
# The audio group is opt-in: it measures real-time playback in headless
# Chromium and its level checks fail intermittently when the machine is busy
# or has no sound device, as in a container.
set -eu

cd "$(dirname "$0")/../.."

check_go() {
    # Only files git knows about or would add: a bare `gofmt -l .` also walks
    # the Flatpak build state and node_modules.
    unformatted=$(git ls-files -co --exclude-standard '*.go' | xargs gofmt -l)
    if [ -n "$unformatted" ]; then
        echo "gofmt needed on:" >&2
        echo "$unformatted" >&2
        exit 1
    fi
    go vet ./...
    xvfb-run -a go test -race -count=1 ./...
    GOOS=windows GOARCH=amd64 go build ./...
}

check_frontend() {
    cd frontend
    npm ci
    npm run check
    npm test
    npm run build
    npm run smoke
    cd ..
}

check_audio() {
    cd frontend
    [ -d node_modules ] || npm ci
    npm run e2e:audio
    cd ..
}

[ $# -gt 0 ] || set -- go frontend
for group in "$@"; do
    case "$group" in
        go) check_go ;;
        frontend) check_frontend ;;
        audio) check_audio ;;
        *) echo "unknown check group: $group" >&2; exit 2 ;;
    esac
done
