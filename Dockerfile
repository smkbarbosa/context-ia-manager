# ---- build stage ----
FROM golang:1.24-bookworm AS builder

# go-sqlite3 requer CGO e gcc
RUN apt-get update && apt-get install -y --no-install-recommends \
    gcc libc6-dev \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .

# Build the API server binary
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/ciam-api ./cmd/server

# Build the CLI binary
RUN CGO_ENABLED=1 GOOS=linux go build -o /out/ciam ./cmd/ciam

# ---- API runtime stage ----
FROM debian:bookworm-slim AS api

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*

COPY --from=builder /out/ciam-api /usr/local/bin/ciam-api

EXPOSE 8080
CMD ["ciam-api"]
