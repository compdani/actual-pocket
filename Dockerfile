# Build Actual Pocket's web client and Go/PocketBase server from source.
FROM node:24.22.1-bookworm AS web
WORKDIR /app
COPY . .
ENV HUSKY=0
ENV YARN_NETWORK_CONCURRENCY=8
RUN yarn install --immutable
# Lage requires a Git HEAD; the real .git is excluded from the build context.
RUN git -c init.defaultBranch=master init -q \
    && git -c user.email=build@docker -c user.name=docker-build add -A \
    && git -c user.email=build@docker -c user.name=docker-build commit -qm build
ENV NODE_OPTIONS=--max_old_space_size=8192
RUN yarn build:browser

FROM golang:1.27.0-bookworm AS gobuilder
WORKDIR /src
COPY packages/sync-server-go/go.mod packages/sync-server-go/go.sum ./
RUN go mod download
COPY packages/sync-server-go/ ./
ARG VERSION=0.0.0-dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.Version=${VERSION}" -o /out/actual-server .
RUN mkdir -p /volumes/data

FROM gcr.io/distroless/static:nonroot AS prod
WORKDIR /app
COPY --from=gobuilder /out/actual-server /actual-server
COPY --from=web /app/packages/desktop-client/build /app/web
COPY --from=gobuilder --chown=65532:65532 /volumes/ /
COPY LICENSE.txt /app/LICENSE.txt
ENV NODE_ENV=production \
    ACTUAL_WEB_ROOT=/app/web \
    ACTUAL_DATA_DIR=/data \
    ACTUAL_HOSTNAME=0.0.0.0
VOLUME ["/data"]
EXPOSE 5006
HEALTHCHECK --interval=30s --timeout=10s --start-period=30s --retries=3 \
    CMD ["/actual-server", "health-check"]
ENTRYPOINT ["/actual-server"]
