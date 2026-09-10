# syntax=docker/dockerfile:1

# YuNet relies on OpenCV FaceDetectorYN. Keep build and runtime on the same
# OpenCV ABI rather than delegating face detection to an external service.
FROM gocv/opencv:4.10.0 AS builder

WORKDIR /app

COPY go.mod ./
COPY go.sum ./
RUN go mod download

COPY . .
RUN go build -mod=readonly -o /bin/server ./cmd/server/

FROM builder AS test
RUN go vet ./... && go test ./...

FROM debian:bullseye-slim

# ffmpeg для видеотранскодирования, jpegtran для progressive JPEG variants,
# libheif для HEIC-снимков, libtbb2, GTK и OpenCV для локального YuNet face
# detection.
#
# Bullseye has moved to the Debian archive: the mirrors no longer refresh its
# release files, so the runtime packages are installed from the archive with the
# validity window switched off. Without this the image cannot be rebuilt at all.
RUN printf 'deb http://archive.debian.org/debian bullseye main\n' > /etc/apt/sources.list && \
    apt-get -o Acquire::Check-Valid-Until=false update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
      libgtk2.0-0 \
      libheif-examples \
      libjpeg-turbo-progs \
      libtbb2 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /usr/local/lib/ /usr/local/lib/
COPY --from=builder /bin/server /app/server
RUN ldconfig

EXPOSE 3000

ENTRYPOINT ["/app/server"]
