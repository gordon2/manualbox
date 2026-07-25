# syntax=docker/dockerfile:1

# manualbox ships as one small image containing the Go binary with the SPA
# embedded, plus the two optional command-line tools that make PDFs and scans
# usable: poppler and tesseract. Nothing else is required at runtime — no
# database server, no cache, no separate web server.

# ---------------------------------------------------------------- frontend ----
FROM node:22-alpine AS web

WORKDIR /src/web
# Copy the manifests alone first so `npm ci` is cached until dependencies change.
COPY web/package.json web/package-lock.json ./
RUN npm ci

COPY web/ ./
# tsconfig/vite reference the output path inside the Go tree, so create it.
RUN mkdir -p /src/internal/frontend/dist
RUN npm run build

# ------------------------------------------------------------------- build ----
FROM golang:1.25-alpine AS build

WORKDIR /src

# Module downloads cache independently of source changes.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Bring in the compiled SPA so go:embed picks it up.
COPY --from=web /src/internal/frontend/dist/app ./internal/frontend/dist/app

ARG VERSION=docker
ARG COMMIT=unknown

# CGO stays off: the SQLite driver is pure Go, which is what lets the binary run
# on a distroless-style image and cross-compile without a toolchain per platform.
RUN CGO_ENABLED=0 go build \
      -trimpath \
      -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
      -o /out/manualbox ./cmd/manualbox

# ----------------------------------------------------------------- runtime ----
FROM alpine:3.21

# Extra OCR languages at build time, e.g. --build-arg TESSERACT_LANGS="eng deu ukr".
# English is always installed because manuals mix it in even when they are not
# written in it. Each language adds a few megabytes.
ARG TESSERACT_LANGS="eng"

RUN apk add --no-cache \
      ca-certificates \
      tzdata \
      poppler-utils \
      tesseract-ocr \
  && for lang in ${TESSERACT_LANGS}; do \
       apk add --no-cache "tesseract-ocr-data-${lang}"; \
     done

# Run as a non-root user. The numeric ID is fixed so a bind-mounted host
# directory can be chowned to something predictable.
RUN addgroup -g 10001 -S manualbox \
  && adduser -u 10001 -S -G manualbox -h /home/manualbox manualbox

COPY --from=build /out/manualbox /usr/local/bin/manualbox

# The data directory holds the SQLite database and the blob store: the entire
# state of the instance, and the only thing that needs backing up.
RUN mkdir -p /data && chown manualbox:manualbox /data
VOLUME ["/data"]

USER manualbox:manualbox
WORKDIR /data

ENV MANUALBOX_DATA_DIR=/data \
    MANUALBOX_ADDR=:7745 \
    MANUALBOX_LOG_FORMAT=json

EXPOSE 7745

# The health endpoint touches the database, so this reports real readiness rather
# than merely that the process is alive.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget -qO- http://127.0.0.1:7745/api/v1/health >/dev/null || exit 1

ENTRYPOINT ["manualbox"]
CMD ["serve"]
