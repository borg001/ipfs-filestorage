# syntax=docker/dockerfile:1

FROM golang:1.23 AS builder

WORKDIR /app

COPY go.mod ./
RUN go mod download

COPY . .
RUN go mod tidy && go build -o /bin/server ./cmd/server/

FROM debian:bookworm-slim

# ffmpeg для видеотранскодирования
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /bin/server /app/server

EXPOSE 3000

ENTRYPOINT ["/app/server"]
