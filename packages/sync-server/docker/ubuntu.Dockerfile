FROM node:24.22.1-bookworm AS builder

# Install required packages
RUN apt-get update && apt-get install -y openssl

WORKDIR /app

COPY pnpm-lock.yaml pnpm-workspace.yaml package.json ./
COPY patches ./patches

RUN corepack enable pnpm
COPY packages ./packages

# Install only the sync server and its workspace dependencies in production
# mode, limiting concurrency on ARMv7 to avoid memory issues.
RUN if [ "$(uname -m)" = "armv7l" ]; then \
      LIMITS="--network-concurrency=5 --child-concurrency=2"; \
    fi; \
    pnpm install --frozen-lockfile --prod --filter "@actual-app/sync-server..." $LIMITS

# Dereference pnpm's workspace:* symlinks so the prod stage can copy just node_modules.
RUN cp -RL node_modules node_modules.real \
    && rm -rf node_modules \
    && mv node_modules.real node_modules

# Strip dev-only content from dereferenced workspace packages to keep the final image lean.
RUN find node_modules/@actual-app -maxdepth 2 -type d \
    \( -name src -o -name e2e -o -name __tests__ -o -name __mocks__ -o -name tests -o -name test -o -name build-stats \) \
    -exec rm -rf {} +

FROM node:24.22.1-bookworm-slim AS prod

# Minimal runtime dependencies
RUN apt-get update && apt-get install tini && apt-get clean -y && rm -rf /var/lib/apt/lists/*

# Create a non-root user
ARG USERNAME=actual
ARG USER_UID=1001
ARG USER_GID=$USER_UID
RUN groupadd --gid $USER_GID $USERNAME \
    && useradd --uid $USER_UID --gid $USER_GID -m $USERNAME \
    && mkdir /data && chown -R ${USERNAME}:${USERNAME} /data

WORKDIR /app
ENV NODE_ENV=production

# sync-server entry flattened at /app so CMD stays `node app.js`.
COPY --from=builder /app/node_modules ./node_modules
COPY --from=builder /app/packages/sync-server/package.json ./
COPY --from=builder /app/packages/sync-server/build ./

# script dir changed when we swapped build method, add the legacy dir in for compatibility
RUN mkdir -p src && ln -s ../scripts src/scripts

ENTRYPOINT ["/usr/bin/tini","-g",  "--"]
EXPOSE 5006
CMD ["node", "app.js"]
