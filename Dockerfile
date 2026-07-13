# FZSM AI dashboard + bot (Go-only)
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go/go.mod go/go.sum ./
RUN go mod download
COPY go/ ./
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/fzsm-bot ./cmd/bot \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/fzsm-dashboard ./cmd/dashboard \
 && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /out/fzsm-doctor ./cmd/doctor

FROM debian:bookworm-slim
WORKDIR /app
RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates tzdata \
 && rm -rf /var/lib/apt/lists/* \
 && mkdir -p /app/bin /app/auth /app/data /app/logs /app/config /app/web /app/scripts
ENV TZ=Asia/Shanghai
COPY --from=build /out/fzsm-bot /app/bin/fzsm-bot
COPY --from=build /out/fzsm-dashboard /app/bin/fzsm-dashboard
COPY --from=build /out/fzsm-doctor /app/bin/fzsm-doctor
COPY config/ /app/config/
COPY web/ /app/web/
COPY scripts/start-server.sh /app/scripts/start-server.sh
RUN chmod +x /app/bin/fzsm-bot /app/bin/fzsm-dashboard /app/bin/fzsm-doctor /app/scripts/start-server.sh
EXPOSE 8787
ENV HOST=0.0.0.0 \
    PORT=8787 \
    FZSM_CONFIG=config/config.yaml \
    ENABLE_BOT=1 \
    BOT_MODE=live \
    BOT_EVERY=18
CMD ["/app/scripts/start-server.sh"]
