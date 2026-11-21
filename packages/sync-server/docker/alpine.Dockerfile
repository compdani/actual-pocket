FROM alpine:3.23 AS builder

# Install required packages
RUN apk add --no-cache nodejs npm python3 openssl build-base

WORKDIR /app

COPY pnpm-lock.yaml pnpm-workspace.yaml package.json ./
COPY patches ./patches
COPY packages ./packages

# Alpine's Node.js package does not include Corepack, so install the pnpm
# release pinned in package.json.
RUN npm install -g "$(node -p "require('./package.json').packageManager")"

# Install only the sync server and its workspace dependencies in production
# mode, limiting concurrency on ARMv7 to avoid memory issues.
RUN if [ "$(uname -m)" = "armv7l" ]; then \
      LIMITS="--network-concurrency=5 --child-concurrency=2"; \
    fi; \
    pnpm install --frozen-lockfile --prod --filter "@actual-app/sync-server..." $LIMITS

# better-sqlite3 only ships prebuilt binaries for x64 and arm64, so compile it
# on any other architecture (armv6/armv7 images).
RUN if [ "$(node node_modules/better-sqlite3/lib/binding.js)" = "0" ]; then \
      cd node_modules/better-sqlite3 && node ../node-gyp/bin/node-gyp.js rebuild --release; \
    fi

# Dereference pnpm's workspace:* symlinks so the prod stage can copy just node_modules.
RUN cp -RL node_modules node_modules.real \
    && rm -rf node_modules \
    && mv node_modules.real node_modules

# Strip dev-only content from dereferenced workspace packages to keep the final image lean.
RUN find node_modules/@actual-app -maxdepth 2 -type d \
    \( -name src -o -name e2e -o -name __tests__ -o -name __mocks__ -o -name tests -o -name test -o -name build-stats \) \
    -exec rm -rf {} +

FROM alpine:3.23 AS prod

# Minimal runtime dependencies
RUN apk add --no-cache nodejs tini

# Create a non-root user
ARG USERNAME=actual
ARG USER_UID=1001
ARG USER_GID=$USER_UID
RUN addgroup -S ${USERNAME} -g ${USER_GID} && adduser -S ${USERNAME} -G ${USERNAME} -u ${USER_UID}
RUN mkdir /data && chown -R ${USERNAME}:${USERNAME} /data

WORKDIR /app
ENV NODE_ENV=production

# sync-server entry flattened at /app so CMD stays `node app.js`.
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/packages/sync-server/package.json ./
COPY --from=builder /app/packages/sync-server/build ./

# script dir changed when we swapped build method, add the legacy dir in for compatibility
RUN mkdir -p src && ln -s ../scripts src/scripts

ENTRYPOINT ["/sbin/tini","-g",  "--"]
EXPOSE 5006
CMD ["node", "app.js"]
