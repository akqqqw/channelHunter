# syntax=docker/dockerfile:1
#
# One image per process (api, bot, worker). Everything is compiled from the same
# source tree in a single stage so the three binaries always share code and
# dependencies; only the final stage differs.
#
#   docker build --build-arg TARGET=api   -t channelhunter-api:local .
#   docker build --build-arg TARGET=bot   -t channelhunter-bot:local .
#   docker build --build-arg TARGET=worker -t channelhunter-worker:local .

# ---- build -----------------------------------------------------------------
FROM golang:1.25-alpine AS build

WORKDIR /src

# Dependencies first so layer caching survives source edits.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG TARGET=api

# CGO is not needed (pgx and the Telegram client are pure Go), and -trimpath
# keeps build paths out of the binary.
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -ldflags="-s -w -X github.com/akqqqw/channelhunter/backend/internal/app.Version=${VERSION}" \
        -o /out/channelhunter-${TARGET} ./cmd/${TARGET}

# ---- runtime ---------------------------------------------------------------
FROM alpine:3.21

# ca-certificates: TLS to the Telegram API and external providers.
# tzdata: report periods are grouped by APP_TIMEZONE.
RUN apk add --no-cache ca-certificates tzdata wget \
    && addgroup -S channelhunter \
    && adduser -S -G channelhunter -h /app channelhunter

WORKDIR /app

ARG TARGET=api
COPY --from=build /out/channelhunter-${TARGET} /app/channelhunter

# Migrations are embedded in the binary; the directory is copied for operators
# who want to inspect or override them.
COPY --from=build /src/migrations /app/migrations

USER channelhunter

# api serves HTTP here; bot and worker expose health probes on the same port.
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD wget -qO- http://127.0.0.1:8080/health || exit 1

ENTRYPOINT ["/app/channelhunter"]
