# Build from the repository root:  docker build -f deploy/Dockerfile -t inbox-server .
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/inboxd ./cmd/inboxd

# Alpine rather than distroless: `docker compose exec inbox sh` and busybox wget (used by the compose health check) are handy.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata && adduser -D -u 10001 inbox
COPY --from=build /out/inboxd /usr/local/bin/inboxd
USER inbox
# INBOX_ENV=production: the server refuses missing or insecure settings instead of warning (docs/DEPLOY.md).
ENV HTTP_ADDR=:8080 INGEST_ADDR=:8081 INBOX_ENV=production
# 8080: the app API (Tailscale Serve). 8081: only /ingest/v1/notify (Tailscale Funnel).
EXPOSE 8080 8081
# `inboxd serve` applies pending schema migrations at start (advisory lock); `inboxd migrate` does the same by hand.
ENTRYPOINT ["inboxd"]
CMD ["serve"]
