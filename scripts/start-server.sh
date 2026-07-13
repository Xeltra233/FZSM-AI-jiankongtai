#!/bin/sh
set -eu
cd /app

mkdir -p /app/auth /app/data /app/logs /app/config /app/web

CFG="${FZSM_CONFIG:-config/config.yaml}"
HOST="${HOST:-0.0.0.0}"
BOT_MODE="${BOT_MODE:-live}"
ENABLE_BOT="${ENABLE_BOT:-1}"
LOG_MAX_AGE_DAYS="${LOG_MAX_AGE_DAYS:-7}"
HTML_PATH="${FZSM_HTML:-web/dashboard.html}"

# Resolve listen port robustly.
# Zeabur/users may set PORT="${WEB_PORT}" literally, or only set WEB_PORT.
is_port() {
  case "${1:-}" in
    ''|*[!0-9]*) return 1 ;;
    *)
      if [ "$1" -ge 1 ] 2>/dev/null && [ "$1" -le 65535 ] 2>/dev/null; then
        return 0
      fi
      return 1
      ;;
  esac
}

RAW_PORT="${PORT:-}"
RAW_WEB_PORT="${WEB_PORT:-}"
RAW_FZSM_PORT="${FZSM_PORT:-}"
PORT="8787"
for cand in "$RAW_PORT" "$RAW_WEB_PORT" "$RAW_FZSM_PORT" "8787"; do
  if is_port "$cand"; then
    PORT="$cand"
    break
  fi
done

RAW_EVERY="${BOT_EVERY:-18}"
case "$RAW_EVERY" in
  ''|*[!0-9]*) BOT_EVERY="18" ;;
  *)
    if [ "$RAW_EVERY" -ge 1 ] 2>/dev/null; then
      BOT_EVERY="$RAW_EVERY"
    else
      BOT_EVERY="18"
    fi
    ;;
esac

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

# Empty web volume mounts hide image files. Seed dashboard assets from baked defaults.
seed_web() {
  mkdir -p /app/web
  if [ ! -f /app/web/dashboard.html ]; then
    if [ -f /app/web.default/dashboard.html ]; then
      echo "seeding web assets from /app/web.default"
      # copy missing files only, never overwrite user customizations
      if command -v cp >/dev/null 2>&1; then
        # shell-compatible recursive seed of missing paths
        (
          cd /app/web.default
          find . -type f 2>/dev/null | while IFS= read -r rel; do
            rel=${rel#./}
            [ -n "$rel" ] || continue
            if [ ! -e "/app/web/$rel" ]; then
              mkdir -p "/app/web/$(dirname "$rel")"
              cp -f "/app/web.default/$rel" "/app/web/$rel" 2>/dev/null || true
            fi
          done
        )
      fi
    fi
  fi
  # final fallback for html path
  if [ ! -f "$HTML_PATH" ] && [ -f /app/web.default/dashboard.html ]; then
    HTML_PATH="/app/web.default/dashboard.html"
    echo "using fallback html: $HTML_PATH"
  fi
}

seed_config
seed_web

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

if [ ! -f "$HTML_PATH" ]; then
  echo "dashboard html not found: $HTML_PATH" >&2
  echo "hint: remove empty web volume mount, or ensure web/dashboard.html exists" >&2
  ls -la /app /app/web /app/web.default 2>/dev/null || true
  exit 1
fi

echo "using config: $CFG"
ls -la "$CFG" || true
echo "using html: $HTML_PATH"
if [ "$RAW_PORT" != "$PORT" ]; then
  echo "PORT normalized: raw='${RAW_PORT}' web_port='${RAW_WEB_PORT}' -> ${PORT}"
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
exec ./bin/fzsm-dashboard -c "$CFG" -host "$HOST" -port "$PORT" -html "$HTML_PATH"