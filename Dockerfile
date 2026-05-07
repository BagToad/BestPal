FROM golang:1.26 AS build

WORKDIR /build

RUN git clone https://github.com/BagToad/BestPal.git && \
    cd BestPal && \
    make build-all
    
RUN mkdir -p /build/logs

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /home/nonroot

COPY --from=build --chown=65532:65532 --chmod=755 /build/BestPal/bin/gamerpal-linux-amd64 /home/nonroot/gamerpal
COPY --from=build --chown=65532:65532 --chmod=644 /build/logs /home/nonroot/logs

VOLUME /home/nonroot/logs

ENV GAMERPAL_BOT_TOKEN=""
ENV GAMERPAL_IGDB_CLIENT_SECRET=""
ENV GAMERPAL_IGDB_CLIENT_TOKEN=""
ENV GAMERPAL_LOG_DIR=""

# Time zone if logs have the wrong time.
ENV TZ=""

ENTRYPOINT [ "/home/nonroot/gamerpal" ]

# Optional health check needs testing though. (If the bot dies it's likely the Docker container will just exit anyways.)
# HEALTHCHECK --interval=60s --timeout=30s --retries=5 \
#  CMD pgrep gamerpal || exit 1
