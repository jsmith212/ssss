# Linux toolchain for ssss: everything needed to build the app and run every
# check, so that CI and a local run use the same environment.
#
#   docker build -t ssss-ci build/ci
#   docker run --rm -u "$(id -u):$(id -g)" -v "$PWD":/src ssss-ci build/ci/check.sh
FROM node:24-trixie-slim AS node

# Debian 13 has the GTK 4 and WebKitGTK 6.0 development packages.
FROM golang:1.26-trixie

RUN apt-get update && apt-get install -y --no-install-recommends \
        pkg-config libgtk-4-dev libwebkitgtk-6.0-dev \
        chromium xvfb xauth \
    && rm -rf /var/lib/apt/lists/*

COPY --from=node /usr/local/bin/node /usr/local/bin/node
COPY --from=node /usr/local/lib/node_modules /usr/local/lib/node_modules
RUN ln -s ../lib/node_modules/npm/bin/npm-cli.js /usr/local/bin/npm \
    && ln -s ../lib/node_modules/npm/bin/npx-cli.js /usr/local/bin/npx

# Keep in step with go.mod and frontend/package.json.
ARG WAILS_VERSION=v3.1.0
RUN GOBIN=/usr/local/bin go install github.com/wailsapp/wails/v3/cmd/wails3@${WAILS_VERSION} \
    && go clean -cache -modcache

# The container runs as the caller's user so files written to the mounted
# checkout keep their owner; that user has no home, so caches go to /tmp.
ENV HOME=/tmp \
    GOCACHE=/tmp/go-cache \
    GOMODCACHE=/tmp/go-mod \
    GOFLAGS=-buildvcs=false \
    npm_config_cache=/tmp/npm-cache \
    CHROMIUM=chromium

WORKDIR /src
