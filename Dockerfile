# syntax=docker/dockerfile:1.6

# ---------- Go build stage ----------
FROM golang:1.26-bookworm AS gobuild

WORKDIR /build

# Cache module downloads in a separate layer keyed only on go.mod/go.sum
# so editing source files doesn't re-download everything.
COPY go.mod go.sum ./
RUN go mod download

# Build the static linux/amd64 binary using the Makefile target.
COPY . .
RUN make build-linux-amd64

# Stage a writable /data directory that is owned by the nonroot user the
# runtime image runs as (uid/gid 65532). Distroless has no shell, so we
# can't mkdir/chown inside the runtime stage.
RUN mkdir -p /staging/data && chown -R 65532:65532 /staging/data

# ---------- Copilot CLI fetch stage ----------
# Pull just the platform-specific binary from npm. The @github/copilot-<plat>-<arch>
# packages are self-contained native executables (Node SEAs); we don't need the
# JS loader or the rest of the @github/copilot package tree at runtime.
FROM node:24-bookworm-slim AS copilot
ARG COPILOT_VERSION=1.0.54
WORKDIR /work
RUN npm pack "@github/copilot-linux-x64@${COPILOT_VERSION}" \
  && tar -xzf "github-copilot-linux-x64-${COPILOT_VERSION}.tgz" \
  && install -m 0755 -o 65532 -g 65532 package/copilot /copilot

# ---------- Runtime stage ----------
# distroless/cc adds glibc + libstdc++/libgcc_s on top of distroless/base, which
# the copilot binary needs (verified via ldd against the v1.0.54 linux-x64 build).
FROM gcr.io/distroless/cc-debian12:nonroot

WORKDIR /home/nonroot

COPY --from=gobuild --chown=65532:65532 /build/bin/gamerpal-linux-amd64 /home/nonroot/gamerpal
COPY --from=gobuild --chown=65532:65532 /staging/data /data
COPY --from=copilot --chown=65532:65532 /copilot /opt/copilot/copilot

# Defaults tuned for container deployments:
# - Log to stdout (captured by the host platform), not to a rotating file.
# - Persist the SQLite database under /data so it can live on a mounted volume.
# - Point the Copilot SDK at the bundled CLI binary.
ENV GAMERPAL_DISABLE_FILE_LOGGING=true
ENV GAMERPAL_DATABASE_PATH=/data/gamerpal.db
ENV COPILOT_CLI_PATH=/opt/copilot/copilot

ENTRYPOINT ["/home/nonroot/gamerpal"]
