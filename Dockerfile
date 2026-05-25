# syntax=docker/dockerfile:1.6

# ---------- build stage ----------
FROM golang:1.26-bookworm AS build

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

# ---------- runtime stage ----------
FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /home/nonroot

COPY --from=build --chown=65532:65532 /build/bin/gamerpal-linux-amd64 /home/nonroot/gamerpal
COPY --from=build --chown=65532:65532 /staging/data /data

# Defaults tuned for container deployments:
# - Log to stdout (captured by the host platform), not to a rotating file.
# - Persist the SQLite database under /data so it can live on a mounted volume.
ENV GAMERPAL_DISABLE_FILE_LOGGING=true
ENV GAMERPAL_DATABASE_PATH=/data/gamerpal.db

ENTRYPOINT ["/home/nonroot/gamerpal"]
