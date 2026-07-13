#!/usr/bin/env python3
"""Local read-only collector for 20 minutes. GET only. No bot write ops."""

from __future__ import annotations

import json
import time
import traceback
from datetime import datetime, timezone, timedelta
from pathlib import Path
from urllib.parse import urljoin
from urllib.request import Request, build_opener, HTTPCookieProcessor
from http.cookiejar import CookieJar
from http.cookiejar import Cookie

ROOT = Path(__file__).resolve().parents[1]
CFG = ROOT / "config" / "config.yaml"
COOKIE_FILE = ROOT / "auth" / "cookies.json"
OUT_DIR = ROOT / "goal-6" / "evidence" / "readonly_20m"
DURATION_SEC = 20 * 60
INTERVAL_SEC = 30

STOCKS_BASE = "https://fanzisima.xyz/stocks/api"
LOTTERY_BASE = "https://api.fanzisima.xyz"

# GET-only endpoints for observation
STOCKS_GETS = [
    "/me",
    "/portfolio",
    "/market",
    "/farm/me",
    "/futures",
    "/margin/positions",
    "/broker/me",
    "/broker/list",
    "/broker/candidates",
    "/broker/underwriter/list",
    "/events",
    "/news",
]
LOTTERY_GETS = [
    "/lottery/api/me",
    "/lottery/api/slot/config",
    "/lottery/api/loan/offers",
    "/lottery/api/vip/state",
    "/lottery/api/vip/my-room",
    "/lottery/api/vip/stats",
    "/lottery/api/vip/history",
]


def now_iso() -> str:
    return datetime.now().astimezone().isoformat(timespec="seconds")


def load_cookies() -> list[dict]:
    if not COOKIE_FILE.exists():
        return []
    raw = json.loads(COOKIE_FILE.read_text(encoding="utf-8"))
    if isinstance(raw, list):
        return [x for x in raw if isinstance(x, dict)]
    if isinstance(raw, dict) and isinstance(raw.get("cookies"), list):
        return [x for x in raw["cookies"] if isinstance(x, dict)]
    if isinstance(raw, dict) and raw.get("name") and raw.get("value"):
        return [raw]
    return []


def build_client():
    jar = CookieJar()
    opener = build_opener(HTTPCookieProcessor(jar))
    for it in load_cookies():
        name = str(it.get("name") or "").strip()
        value = str(it.get("value") or "").strip()
        if not name or not value:
            continue
        domain = str(it.get("domain") or ".fanzisima.xyz").strip() or ".fanzisima.xyz"
        path = str(it.get("path") or "/").strip() or "/"
        # Cookie(version, name, value, port, port_specified, domain, domain_specified,
        # domain_initial_dot, path, path_specified, secure, expires, discard, comment,
        # comment_url, rest, rfc2109)
        c = Cookie(
            0, name, value, None, False,
            domain, bool(domain), domain.startswith("."),
            path, True, False, None, False, None, None, {}, False,
        )
        jar.set_cookie(c)
    return opener


def http_get(opener, base: str, path: str, timeout: float = 12.0) -> dict:
    url = urljoin(base.rstrip("/") + "/", path.lstrip("/")) if path.startswith("http") else (base.rstrip("/") + "/" + path.lstrip("/"))
    # fix double
    if base.endswith("/api") and path.startswith("/"):
        url = base.rstrip("/") + path
    if "lottery" in base and path.startswith("/lottery"):
        url = base.rstrip("/") + path
    headers = {
        "User-Agent": "fzsm-readonly-collector/0.1",
        "Accept": "application/json, text/plain, */*",
    }
    if "stocks" in base:
        headers["Origin"] = "https://fanzisima.xyz"
        headers["Referer"] = "https://fanzisima.xyz/stocks/"
    else:
        headers["Origin"] = "https://api.fanzisima.xyz"
        headers["Referer"] = "https://api.fanzisima.xyz/lottery/page"
    req = Request(url, headers=headers, method="GET")
    t0 = time.time()
    try:
        with opener.open(req, timeout=timeout) as resp:
            body = resp.read()
            latency_ms = int((time.time() - t0) * 1000)
            text = body.decode("utf-8", errors="replace")
            data = None
            try:
                data = json.loads(text)
            except Exception:
                data = {"_raw": text[:500]}
            return {
                "ok": 200 <= resp.status < 400,
                "status": resp.status,
                "latency_ms": latency_ms,
                "url": url,
                "data": data,
            }
    except Exception as e:
        latency_ms = int((time.time() - t0) * 1000)
        return {
            "ok": False,
            "status": 0,
            "latency_ms": latency_ms,
            "url": url,
            "error": str(e),
        }


def pick(d, *keys, default=None):
    if not isinstance(d, dict):
        return default
    for k in keys:
        if k in d and d[k] is not None:
            return d[k]
    return default


def unwrap(data):
    if isinstance(data, dict) and "data" in data and isinstance(data["data"], (dict, list)):
        return data["data"]
    return data


def summarize_tick(stocks: dict, lottery: dict) -> dict:
    me = unwrap((stocks.get("/me") or {}).get("data"))
    port = unwrap((stocks.get("/portfolio") or {}).get("data"))
    market = unwrap((stocks.get("/market") or {}).get("data"))
    farm = unwrap((stocks.get("/farm/me") or {}).get("data"))
    fut = unwrap((stocks.get("/futures") or {}).get("data"))
    mpos = unwrap((stocks.get("/margin/positions") or {}).get("data"))
    bme = unwrap((stocks.get("/broker/me") or {}).get("data"))
    uw = unwrap((stocks.get("/broker/underwriter/list") or {}).get("data"))
    lotme = unwrap((lottery.get("/lottery/api/me") or {}).get("data"))
    vip_state = unwrap((lottery.get("/lottery/api/vip/state") or {}).get("data"))
    vip_room = unwrap((lottery.get("/lottery/api/vip/my-room") or {}).get("data"))
    slot_cfg = unwrap((lottery.get("/lottery/api/slot/config") or {}).get("data"))
    offers = unwrap((lottery.get("/lottery/api/loan/offers") or {}).get("data"))

    # normalize common fields
    cash = pick(port, "cash", default=pick(me, "cash", "balance", "balance_lobster"))
    equity = pick(port, "equity", "total", "net_asset")
    positions = pick(port, "positions", default=[])
    if not isinstance(positions, list):
        positions = []
    stocks_n = 0
    if isinstance(market, dict):
        arr = market.get("stocks") or market.get("list") or []
        if isinstance(arr, list):
            stocks_n = len(arr)
    elif isinstance(market, list):
        stocks_n = len(market)

    farm_plots = {}
    if isinstance(farm, dict):
        farm_plots = {
            "empty": pick(farm, "empty", default=None),
            "growing": pick(farm, "growing", default=None),
            "ready": pick(farm, "ready", default=None),
            "crop": pick(farm, "crop", "crop_key", default=None),
            "steal_left": pick(farm, "steal_left", "steal_remaining", default=None),
        }

    vip = {}
    if isinstance(vip_state, dict):
        rooms = vip_state.get("rooms") or vip_state.get("public_rooms") or []
        vip = {
            "can_enter": vip_state.get("can_enter"),
            "min_balance": vip_state.get("min_balance"),
            "balance_lobster": vip_state.get("balance_lobster"),
            "rooms": len(rooms) if isinstance(rooms, list) else 0,
            "my_room_id": pick(vip_room if isinstance(vip_room, dict) else {}, "room_id", "id"),
        }

    slot = {}
    if isinstance(slot_cfg, dict):
        slot = {
            "keys": list(slot_cfg.keys())[:20],
            "bet": pick(slot_cfg, "bet", "base_bet", "spin_bet"),
            "rtp": pick(slot_cfg, "rtp", "theory_rtp"),
        }

    return {
        "cash": cash,
        "equity": equity,
        "positions_n": len(positions),
        "market_stocks_n": stocks_n,
        "farm": farm_plots,
        "futures_n": len(fut) if isinstance(fut, list) else (len(fut.get("contracts") or []) if isinstance(fut, dict) else None),
        "margin_positions_n": len(mpos) if isinstance(mpos, list) else None,
        "broker_signed": bool(pick(bme if isinstance(bme, dict) else {}, "signed_broker")),
        "underwriter_n": len(uw) if isinstance(uw, list) else None,
        "lottery": {
            "free_draws": pick(lotme if isinstance(lotme, dict) else {}, "draws_available", "free_draws"),
            "premium_free": pick(lotme if isinstance(lotme, dict) else {}, "draws_available_premium", "premium_free"),
            "checked_today": pick(lotme if isinstance(lotme, dict) else {}, "checked_today", "checkin_done"),
        },
        "vip": vip,
        "slot": slot,
        "loan_offers_n": len(offers) if isinstance(offers, list) else (len(offers.get("offers") or offers.get("list") or []) if isinstance(offers, dict) else None),
    }


def endpoint_health(block: dict) -> dict:
    out = {}
    for path, res in block.items():
        out[path] = {
            "ok": bool(res.get("ok")),
            "status": res.get("status"),
            "latency_ms": res.get("latency_ms"),
            "error": res.get("error"),
        }
    return out


def main():
    OUT_DIR.mkdir(parents=True, exist_ok=True)
    opener = build_client()
    started = time.time()
    end_at = started + DURATION_SEC
    ticks = []
    print(f"[readonly] start {now_iso()} duration={DURATION_SEC}s interval={INTERVAL_SEC}s", flush=True)
    print("[readonly] bot write ops DISABLED (collector GET only)", flush=True)

    i = 0
    while time.time() < end_at:
        i += 1
        t0 = time.time()
        tick = {
            "i": i,
            "ts": now_iso(),
            "epoch": t0,
            "stocks": {},
            "lottery": {},
            "errors": [],
        }
        for path in STOCKS_GETS:
            tick["stocks"][path] = http_get(opener, STOCKS_BASE, path)
        for path in LOTTERY_GETS:
            # lottery base is host root
            tick["lottery"][path] = http_get(opener, LOTTERY_BASE, path)

        try:
            tick["summary"] = summarize_tick(tick["stocks"], tick["lottery"])
        except Exception as e:
            tick["summary_error"] = str(e)
            tick["errors"].append(traceback.format_exc(limit=3))
        tick["health"] = {
            "stocks": endpoint_health(tick["stocks"]),
            "lottery": endpoint_health(tick["lottery"]),
        }
        # compact raw for disk: drop large bodies, keep summary + health + tiny snippets
        compact = {
            "i": tick["i"],
            "ts": tick["ts"],
            "epoch": tick["epoch"],
            "summary": tick.get("summary"),
            "health": tick.get("health"),
            "errors": tick.get("errors"),
        }
        ticks.append(compact)
        # write progressive snapshot
        (OUT_DIR / "ticks.jsonl").open("a", encoding="utf-8").write(json.dumps(compact, ensure_ascii=False) + "\n")
        (OUT_DIR / "latest.json").write_text(json.dumps(compact, ensure_ascii=False, indent=2), encoding="utf-8")

        ok_s = sum(1 for v in tick["health"]["stocks"].values() if v.get("ok"))
        ok_l = sum(1 for v in tick["health"]["lottery"].values() if v.get("ok"))
        s = tick.get("summary") or {}
        print(
            f"[readonly] #{i} stocks {ok_s}/{len(STOCKS_GETS)} lottery {ok_l}/{len(LOTTERY_GETS)} "
            f"cash={s.get('cash')} equity={s.get('equity')} pos={s.get('positions_n')} "
            f"vip_rooms={(s.get('vip') or {}).get('rooms')} free={(s.get('lottery') or {}).get('free_draws')}",
            flush=True,
        )

        # sleep remaining interval
        spent = time.time() - t0
        sleep_for = max(1.0, INTERVAL_SEC - spent)
        # don't overshoot too much
        if time.time() + sleep_for > end_at:
            sleep_for = max(0.0, end_at - time.time())
        if sleep_for > 0:
            time.sleep(sleep_for)

    # final aggregate
    def lat_stats(path_group: str):
        vals = []
        oks = 0
        total = 0
        for t in ticks:
            for path, h in ((t.get("health") or {}).get(path_group) or {}).items():
                total += 1
                if h.get("ok"):
                    oks += 1
                if isinstance(h.get("latency_ms"), int):
                    vals.append(h["latency_ms"])
        vals.sort()
        def pct(p):
            if not vals:
                return None
            idx = min(len(vals) - 1, max(0, int(round((p/100) * (len(vals)-1)))))
            return vals[idx]
        return {
            "ok": oks,
            "total": total,
            "ok_rate": (oks / total) if total else 0,
            "latency_p50": pct(50),
            "latency_p95": pct(95),
            "latency_max": vals[-1] if vals else None,
        }

    # equity/cash series
    series = []
    for t in ticks:
        s = t.get("summary") or {}
        series.append({
            "ts": t.get("ts"),
            "cash": s.get("cash"),
            "equity": s.get("equity"),
            "positions_n": s.get("positions_n"),
            "vip_rooms": (s.get("vip") or {}).get("rooms"),
            "free_draws": (s.get("lottery") or {}).get("free_draws"),
            "loan_offers_n": s.get("loan_offers_n"),
            "farm": s.get("farm"),
        })

    # instability / flapping endpoints
    flap = {}
    for group in ("stocks", "lottery"):
        paths = set()
        for t in ticks:
            paths.update(((t.get("health") or {}).get(group) or {}).keys())
        for path in sorted(paths):
            seq = []
            for t in ticks:
                h = ((t.get("health") or {}).get(group) or {}).get(path) or {}
                seq.append(bool(h.get("ok")))
            changes = sum(1 for i in range(1, len(seq)) if seq[i] != seq[i-1])
            flap[f"{group}:{path}"] = {
                "ok_count": sum(seq),
                "n": len(seq),
                "changes": changes,
                "always_ok": all(seq) if seq else False,
                "always_fail": (not any(seq)) if seq else False,
            }

    report = {
        "started_at": datetime.fromtimestamp(started).astimezone().isoformat(timespec="seconds"),
        "ended_at": now_iso(),
        "duration_sec": int(time.time() - started),
        "interval_sec": INTERVAL_SEC,
        "ticks": len(ticks),
        "mode": "readonly_get_only",
        "bot_write_ops": False,
        "health": {
            "stocks": lat_stats("stocks"),
            "lottery": lat_stats("lottery"),
        },
        "series": series,
        "endpoint_stability": flap,
        "latest": ticks[-1] if ticks else None,
        "notes": [
            "Collector uses GET only; no POST/buy/sell/join/bet/plant.",
            "Local bot should remain stopped to avoid competing with cloud writes.",
        ],
    }
    (OUT_DIR / "summary.json").write_text(json.dumps(report, ensure_ascii=False, indent=2), encoding="utf-8")
    print(f"[readonly] done ticks={len(ticks)} summary={OUT_DIR / 'summary.json'}", flush=True)


if __name__ == "__main__":
    main()
