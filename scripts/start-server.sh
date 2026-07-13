#!/bin/sh
set -eu
cd /app

mkdir -p /app/auth /app/data /app/logs /app/config

CFG="${FZSM_CONFIG:-config/config.yaml}"
HOST="${HOST:-0.0.0.0}"
PORT="${PORT:-8787}"
BOT_MODE="${BOT_MODE:-live}"
BOT_EVERY="${BOT_EVERY:-18}"
ENABLE_BOT="${ENABLE_BOT:-1}"
LOG_MAX_AGE_DAYS="${LOG_MAX_AGE_DAYS:-7}"

# If user mounted an empty config volume, seed defaults from image.
seed_config() {
  if [ ! -f "$CFG" ]; then
    echo "config missing: $CFG"
    if [ -f /app/config.default/config.yaml ]; then
      echo "seeding default config into $(dirname "$CFG")"
      mkdir -p "$(dirname "$CFG")"
      cp -f /app/config.default/config.yaml "$CFG"
    elif [ -f /app/config.default/config/config.yaml ]; then
      mkdir -p "$(dirname "$CFG")"
      cp -f /app/config.default/config/config.yaml "$CFG"
    fi
  fi
  # also seed any other default files if target dir empty-ish
  if [ -d /app/config.default ]; then
    for f in /app/config.default/*; do
      [ -e "$f" ] || continue
      base=$(basename "$f")
      if [ ! -e "/app/config/$base" ]; then
        cp -a "$f" "/app/config/$base" 2>/dev/null || true
      fi
    done
  fi
}
seed_config

# shell-side log cleanup (for redirected *.out.log/*.err.log and old rotations)
cleanup_logs() {
  find /app/logs -type f \( -name "*.log" -o -name "*.log.*" -o -name "*.out.log" -o -name "*.err.log" \) -mtime +"${LOG_MAX_AGE_DAYS}" -print -delete 2>/dev/null || true
  ls -1t /app/logs/*.[0-9]* 2>/dev/null | tail -n +21 | xargs -r rm -f 2>/dev/null || true
}
cleanup_logs

if [ ! -f "$CFG" ]; then
  echo "config not found after seed: $CFG" >&2
  echo "hint: mount ./config to /app/config OR remove empty config volume so image default can be used" >&2
  ls -la /app /app/config /app/config.default 2>/dev/null || true
  exit 1
fi

echo "using config: $CFG"
ls -la "$CFG" || true

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
