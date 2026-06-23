# syntax=docker/dockerfile:1.7

# ---- Build stage ----
FROM golang:1.26-alpine AS builder

WORKDIR /src

# Oto uses ALSA through cgo on Linux.
RUN apk add --no-cache build-base pkgconf alsa-lib-dev

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=1 go build \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/defqon-recorder ./cmd/recorder

# ---- Runtime stage ----
FROM alpine:3.20

# yt-dlp and FFmpeg are required at runtime to download/convert streams.
RUN apk add --no-cache ca-certificates ffmpeg yt-dlp alsa-lib \
    && addgroup -S app && adduser -S -G app app

WORKDIR /app
COPY --from=builder /out/defqon-recorder /usr/local/bin/defqon-recorder
COPY dq-timetable.json /app/dq-timetable.json

USER app

# Recordings are written here; mount a volume to persist them.
ENV RECORDINGS_DIR=/app/recordings
VOLUME ["/app/recordings"]

ENTRYPOINT ["defqon-recorder"]
