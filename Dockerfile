# FZSM AI dashboard + bot (Go-only, Docker)
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go/go.mod go/go.sum ./
RUN go mod download
COPY go/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/fzsm-bot ./cmd/bot \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/fzsm-dashboard ./cmd/dashboard \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o /out/fzsm-doctor ./cmd/doctor

FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata curl findutils \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /app/bin /app/auth /app/data /app/logs /app/config /app/config.default /app/web /app/web.default /app/scripts
ENV TZ=Asia/Shanghai
COPY --from=build /out/fzsm-bot /app/bin/fzsm-bot
COPY --from=build /out/fzsm-dashboard /app/bin/fzsm-dashboard
COPY --from=build /out/fzsm-doctor /app/bin/fzsm-doctor
# baked defaults (used when mounted empty volumes)
COPY config/ /app/config.default/
COPY config/ /app/config/
COPY web/ /app/web.default/
COPY web/ /app/web/
COPY scripts/start-server.sh /app/scripts/start-server.sh
RUN chmod +x /app/bin/fzsm-bot /app/bin/fzsm-dashboard /app/bin/fzsm-doctor /app/scripts/start-server.sh \
 && sed -i 's/\r$//' /app/scripts/start-server.sh
EXPOSE 8787
ENV HOST=0.0.0.0 \
    PORT=8787 \
    FZSM_CONFIG=config/config.yaml \
    ENABLE_BOT=1 \
    BOT_MODE=live \
    BOT_EVERY=18 \
    LOG_MAX_AGE_DAYS=7
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD curl -fsS "http://127.0.0.1:8787/api/health" || exit 1
CMD ["/app/scripts/start-server.sh"]