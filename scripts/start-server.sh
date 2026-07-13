#!/bin/sh
set -eu
cd /app

mkdir -p /app/auth /app/data /app/logs

CFG="${FZSM_CONFIG:-config/config.yaml}"
HOST="${HOST:-0.0.0.0}"
PORT="${PORT:-8787}"
BOT_MODE="${BOT_MODE:-live}"
BOT_EVERY="${BOT_EVERY:-18}"
ENABLE_BOT="${ENABLE_BOT:-1}"

if [ ! -f "$CFG" ]; then
  echo "config not found: $CFG" >&2
  exit 1
fi

if [ "$ENABLE_BOT" = "1" ] || [ "$ENABLE_BOT" = "true" ] || [ "$ENABLE_BOT" = "TRUE" ]; then
  echo "starting fzsm-bot (primary mode=$BOT_MODE every=${BOT_EVERY}s)"
  ./bin/fzsm-bot -c "$CFG" -primary -mode "$BOT_MODE" -every "$BOT_EVERY" \
    >>/app/logs/bot.out.log 2>>/app/logs/bot.err.log &
  echo $! >/app/logs/bot.pid
else
  echo "ENABLE_BOT=$ENABLE_BOT, skip bot process"
fi

echo "starting fzsm-dashboard on ${HOST}:${PORT}"
exec ./bin/fzsm-dashboard -c "$CFG" -host "$HOST" -port "$PORT" -html web/dashboard.html
