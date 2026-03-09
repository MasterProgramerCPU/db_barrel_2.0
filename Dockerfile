FROM golang:1.25-bookworm AS builder

WORKDIR /src

RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc \
    libc6-dev \
    && rm -rf /var/lib/apt/lists/*

COPY go.mod go.sum ./
RUN go mod download

COPY . .

# CGO is required by go-sqlite3.
RUN CGO_ENABLED=1 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/db-barrel .

FROM debian:bookworm-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && useradd --system --uid 10001 --create-home --home-dir /home/dbbarrel dbbarrel \
    && mkdir -p /config /data \
    && chown -R dbbarrel:dbbarrel /config /data

COPY --from=builder /out/db-barrel /usr/local/bin/db-barrel
COPY databases.docker.json /config/databases.json
COPY testdata/test_barrel.db /data/test_barrel.db

EXPOSE 30000

USER dbbarrel

ENTRYPOINT ["/usr/local/bin/db-barrel"]
CMD ["-port", "30000", "-config", "/config/databases.json"]
