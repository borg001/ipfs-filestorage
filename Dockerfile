# syntax=docker/dockerfile:1

# YuNet relies on OpenCV FaceDetectorYN. Keep build and runtime on the same
# OpenCV ABI rather than delegating face detection to an external service.
FROM gocv/opencv:4.13.0 AS builder

# go.mod needs a newer Go than the image ships; this exact toolchain (the one
# docs/infra/build.py uses for the other Go services) is fetched and
# verified by the go command itself (sum.golang.org).
ENV GOTOOLCHAIN=go1.27.3

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .
RUN go build -mod=readonly -o /bin/server ./cmd/server/

FROM builder AS test
RUN go vet ./... && go test ./...

FROM debian:bookworm-slim

# ffmpeg для видеотранскодирования, jpegtran для progressive JPEG variants,
# libheif для HEIC-снимков, libtbb12, GTK и OpenCV для локального YuNet face
# detection. The runtime is the builder's Debian release, so OpenCV in
# /usr/local/lib finds the same libraries it was linked with.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
      libgtk2.0-0 \
      libheif-examples \
      libjpeg-turbo-progs \
      libtbb12 \
      libwebpdemux2 \
    && apt-get upgrade -y --no-install-recommends \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /usr/local/lib/ /usr/local/lib/
COPY --from=builder /bin/server /app/server
RUN ldconfig

EXPOSE 3000

ENTRYPOINT ["/app/server"]
