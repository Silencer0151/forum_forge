# Stage 1: build the binary
FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /forum ./cmd/forum

# Stage 2: minimal runtime image
FROM alpine:3.21

RUN apk add --no-cache ca-certificates tzdata

RUN addgroup -S forum && adduser -S forum -G forum

WORKDIR /app

COPY --from=builder /forum /app/forum

RUN mkdir -p /app/data /app/uploads /app/certs && \
    chown -R forum:forum /app

USER forum

EXPOSE 80 443

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD wget -qO- http://localhost:8080/health || exit 1

ENV FORUM_LISTEN_ADDR=:8080 \
    FORUM_DB_PATH=/app/data/forum.db \
    FORUM_UPLOAD_PATH=/app/uploads \
    FORUM_TLS_CERT_DIR=/app/certs \
    FORUM_LOG_FORMAT=json

ENTRYPOINT ["/app/forum"]
