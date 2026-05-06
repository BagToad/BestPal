FROM ubuntu:25.10 AS build

RUN apt-get update -y && \
    apt-get install make git libc6-dev golang -y

WORKDIR /build

RUN git clone https://github.com/BagToad/BestPal.git && \
    cd BestPal && \
    make build-all

FROM ubuntu:25.10

RUN apt-get update -y && \
    apt-get install ca-certificates -y
    
RUN update-ca-certificates

COPY --from=build /build/BestPal/bin/gamerpal-linux-amd64 /app/gamerpal

WORKDIR /gamerpal

RUN chown 1000:1000 .

ENV GAMERPAL_BOT_TOKEN=""
ENV GAMERPAL_IGDB_CLIENT_SECRET=""
ENV GAMERPAL_IGDB_CLIENT_TOKEN=""
ENV GAMERPAL_LOG_DIR=""

ENTRYPOINT [ "/app/gamerpal" ]

# Optional health check needs testing though. (If the bot dies it's likely the Docker container will just exit anyways.)
# HEALTHCHECK --interval=60s --timeout=30s --retries=5 \
#  CMD pgrep gamerpal || exit 1
