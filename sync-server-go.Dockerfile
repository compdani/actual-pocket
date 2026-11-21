# Build the Go sync server plus the web client. The build context is the repo
# root because the client is built from the workspace.
FROM node:24.22.1-bookworm AS web

RUN apt-get update && apt-get install -y git && apt-get clean -y && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY .yarn ./.yarn
COPY yarn.lock package.json .yarnrc.yml tsconfig.json lage.config.js ./
COPY packages/api/package.json packages/api/package.json
COPY packages/component-library/package.json packages/component-library/package.json
COPY packages/crdt/package.json packages/crdt/package.json
COPY packages/desktop-client/package.json packages/desktop-client/package.json
COPY packages/desktop-electron/package.json packages/desktop-electron/package.json
COPY packages/eslint-plugin-actual/package.json packages/eslint-plugin-actual/package.json
COPY packages/loot-core/package.json packages/loot-core/package.json
COPY packages/sync-server/package.json packages/sync-server/package.json
COPY packages/plugins-service/package.json packages/plugins-service/package.json
COPY packages/vite-plugin-peggy/package.json packages/vite-plugin-peggy/package.json
COPY bin/package-browser bin/package-browser

RUN yarn install

COPY packages/ ./packages/

# lage needs a git repository for its task hasher.
RUN git -c init.defaultBranch=master init -q \
    && git -c user.email=build@docker -c user.name=docker-build add -A \
    && git -c user.email=build@docker -c user.name=docker-build commit -qm build

ENV NODE_OPTIONS=--max_old_space_size=8192
RUN corepack yarn workspace plugins-service build
RUN corepack yarn workspace @actual-app/web build:browser

FROM golang:1.27.0-bookworm AS gobuilder

WORKDIR /src

COPY packages/sync-server-go/go.mod packages/sync-server-go/go.sum ./
RUN go mod download

COPY packages/sync-server-go/ ./

ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /out/actual-server .

# Stage the volume directory so the distroless runtime needs no shell.
RUN mkdir -p /volumes/data

FROM gcr.io/distroless/static:nonroot AS prod

WORKDIR /app

COPY --from=gobuilder /out/actual-server /actual-server
COPY --from=web /app/packages/desktop-client/build /app/web
COPY --from=gobuilder --chown=65532:65532 /volumes/ /

ENV NODE_ENV=production \
    ACTUAL_WEB_ROOT=/app/web \
    ACTUAL_DATA_DIR=/data \
    ACTUAL_HOSTNAME=0.0.0.0

VOLUME ["/data"]
EXPOSE 5006

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
    CMD ["/actual-server", "health-check"]

ENTRYPOINT ["/actual-server"]
