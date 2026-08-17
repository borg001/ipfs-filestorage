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
# libtbb2, GTK и OpenCV для локального YuNet face detection.
RUN apt-get update && \
    apt-get install -y --no-install-recommends \
      ca-certificates \
      ffmpeg \
      libgtk2.0-0 \
      libjpeg-turbo-progs \
      libtbb2 \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /app
COPY --from=builder /usr/local/lib/ /usr/local/lib/
COPY --from=builder /bin/server /app/server
RUN ldconfig

EXPOSE 3000

ENTRYPOINT ["/app/server"]
