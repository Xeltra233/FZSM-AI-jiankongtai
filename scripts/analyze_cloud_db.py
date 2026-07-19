#!/usr/bin/env python3
"""只读分析 FZSM bot 云端数据库拷贝，输出脱敏汇总。"""

from __future__ import annotations

import argparse
import json
import math
import re
import sqlite3
from collections import Counter, defaultdict
from datetime import datetime, timezone
from pathlib import Path
from statistics import median
from typing import Any


SUCCESS = {"submitted", "filled", "ok", "success"}


def load_json(raw: Any, default: Any = None) -> Any:
    if raw is None:
        return default
    try:
        return json.loads(raw)
    except (TypeError, ValueError):
        return default


def iso(ts: Any) -> str | None:
    try:
        return datetime.fromtimestamp(float(ts), timezone.utc).astimezone().isoformat()
    except (TypeError, ValueError, OSError):
        return None


def f(value: Any) -> float:
    try:
        out = float(value)
        return out if math.isfinite(out) else 0.0
    except (TypeError, ValueError):
        return 0.0


def classify_reason(reason: str) -> str:
    text = reason or ""
    rules = (
        ("sell_insufficient", r"可卖股数不足"),
        ("buy_limit_up", r"涨停"),
        ("buy_order_limit", r"单笔上限|买入数量超过上限"),
        ("sell_limit_down", r"跌停"),
        ("bankruptcy_cooldown", r"破产冷却"),
        ("rate_limit", r"429|频繁|限流"),
    )
    for label, pattern in rules:
        if re.search(pattern, text, re.I):
            return label
    return "other"


def state(con: sqlite3.Connection, key: str) -> dict[str, Any]:
    row = con.execute("SELECT value FROM runtime_state WHERE key=?", (key,)).fetchone()
    value = load_json(row[0] if row else None, {})
    return value if isinstance(value, dict) else {}


def analyze(db_path: Path) -> dict[str, Any]:
    uri = f"file:{db_path.resolve().as_posix()}?mode=ro"
    con = sqlite3.connect(uri, uri=True)
    con.row_factory = sqlite3.Row

    integrity = con.execute("PRAGMA integrity_check").fetchone()[0]
    counts = {
        table: con.execute(f"SELECT COUNT(*) FROM {table}").fetchone()[0]
        for table in ("runtime_state", "signals", "snapshots", "trades", "paper_state")
    }

    ranges = {}
    for table in ("signals", "snapshots", "trades"):
        lo, hi = con.execute(f"SELECT MIN(ts), MAX(ts) FROM {table}").fetchone()
        ranges[table] = {"min_ts": lo, "max_ts": hi, "min_at": iso(lo), "max_at": iso(hi)}

    capital_samples: list[dict[str, float]] = []
    for row in con.execute("SELECT ts,payload FROM snapshots ORDER BY id"):
        payload = load_json(row["payload"], {})
        account = payload.get("account", {}) if isinstance(payload, dict) else {}
        cash, equity = f(account.get("cash")), f(account.get("equity"))
        stock_value = f(account.get("stock_value"))
        if equity <= 0:
            equity = cash + stock_value
        if equity > 0:
            capital_samples.append(
                {
                    "ts": f(row["ts"]),
                    "cash": cash,
                    "equity": equity,
                    "stock_value": stock_value,
                    "invested_pct": stock_value / equity,
                    "cash_pct": cash / equity,
                    "positions": float(len(account.get("positions", []) or [])),
                }
            )

    invested = [x["invested_pct"] for x in capital_samples]
    cash_pct = [x["cash_pct"] for x in capital_samples]
    latest_capital = capital_samples[-1] if capital_samples else {}

    trade_rows = list(con.execute("SELECT ts,code,side,shares,price,status,reason FROM trades ORDER BY id"))
    status_counts = Counter((row["status"] or "unknown").lower() for row in trade_rows)
    side_status = Counter((row["side"] or "unknown", (row["status"] or "unknown").lower()) for row in trade_rows)
    error_categories = Counter(
        classify_reason(row["reason"] or "")
        for row in trade_rows
        if (row["status"] or "").lower() not in SUCCESS
    )
    requested_notional = defaultdict(float)
    for row in trade_rows:
        requested_notional[(row["side"] or "unknown", (row["status"] or "unknown").lower())] += f(row["shares"]) * f(row["price"])

    by_code: dict[str, Counter[str]] = defaultdict(Counter)
    for row in trade_rows:
        by_code[row["code"] or "unknown"][(row["status"] or "unknown").lower()] += 1
    worst_codes = sorted(
        (
            {"code": code, "errors": sum(v for k, v in ctr.items() if k not in SUCCESS), "success": sum(v for k, v in ctr.items() if k in SUCCESS)}
            for code, ctr in by_code.items()
        ),
        key=lambda x: (-x["errors"], x["code"]),
    )[:15]

    signal_actions = {}
    for row in con.execute("SELECT action,COUNT(*) n,AVG(score) avg_score,AVG(confidence) avg_conf FROM signals GROUP BY action"):
        signal_actions[row["action"]] = {"count": row["n"], "avg_score": row["avg_score"], "avg_confidence": row["avg_conf"]}

    control = state(con, "control")
    flags = state(con, "feature_flags")
    last_loop = state(con, "last_loop")
    derivatives = state(con, "modules.derivatives")
    slot = state(con, "lottery.slot_edge")
    free_draw = state(con, "risk.obs.free_draw")
    premium_draw = state(con, "risk.obs.free_draw_premium")
    farm_harvest = state(con, "risk.obs.farm_harvest")
    farm_steal = state(con, "risk.obs.farm_steal")

    def obs_summary(obj: dict[str, Any]) -> dict[str, Any]:
        return {
            "source": obj.get("source"),
            "predictive": obj.get("predictive"),
            "samples": int(f(obj.get("samples"))),
            "wins": int(f(obj.get("wins"))),
            "win_rate": f(obj.get("win_rate")),
            "sum_delta": f(obj.get("sum_delta")),
            "obs_ev": f(obj.get("obs_ev")),
        }

    d_analysis = derivatives.get("analysis", {}) if isinstance(derivatives.get("analysis"), dict) else {}
    d_edge = derivatives.get("risk_edge", {}) if isinstance(derivatives.get("risk_edge"), dict) else {}
    account = last_loop.get("account", {}) if isinstance(last_loop.get("account"), dict) else {}

    return {
        "database": {
            "path": db_path.name,
            "source": "cloud_copy",
            "size_bytes": db_path.stat().st_size,
            "integrity": integrity,
            "counts": counts,
            "ranges": ranges,
        },
        "capital": {
            "samples": len(capital_samples),
            "latest": latest_capital,
            "latest_at": iso(latest_capital.get("ts")),
            "invested_pct_min": min(invested, default=0),
            "invested_pct_median": median(invested) if invested else 0,
            "invested_pct_max": max(invested, default=0),
            "cash_pct_median": median(cash_pct) if cash_pct else 0,
            "latest_position_count": len(account.get("positions", []) or []),
            "control": control,
        },
        "trading": {
            "status_counts": dict(status_counts),
            "success_rate": sum(status_counts[k] for k in SUCCESS) / max(1, len(trade_rows)),
            "side_status": {f"{side}:{status}": n for (side, status), n in sorted(side_status.items())},
            "error_categories": dict(error_categories),
            "requested_notional": {f"{side}:{status}": round(value, 2) for (side, status), value in sorted(requested_notional.items())},
            "worst_codes": worst_codes,
            "signal_actions": signal_actions,
        },
        "leverage": {
            "flag_enabled": bool(flags.get("derivatives.trade_enabled")),
            "module_status": derivatives.get("status"),
            "trade_enabled": derivatives.get("trade_enabled"),
            "executable": d_analysis.get("executable"),
            "executable_gap": d_analysis.get("executable_gap", []),
            "edge_gate": d_edge.get("gate"),
            "edge_ok": d_edge.get("edge_ok"),
            "path_notes": derivatives.get("path_notes", []),
        },
        "lottery": {
            "slot": {
                "source": slot.get("source"),
                "theory_rtp": f(slot.get("theory_rtp")),
                "theory_ev_per_spin": f(slot.get("theory_ev_per_spin")),
                "samples": int(f(slot.get("samples"))),
                "edge_ok": slot.get("edge_ok"),
                "gate": slot.get("gate"),
            },
            "free_draw": obs_summary(free_draw),
            "free_draw_premium": obs_summary(premium_draw),
        },
        "other_profit_observations": {
            "farm_harvest": obs_summary(farm_harvest),
            "farm_steal": obs_summary(farm_steal),
        },
    }


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("db", type=Path)
    parser.add_argument("--out", type=Path)
    args = parser.parse_args()
    report = analyze(args.db)
    text = json.dumps(report, ensure_ascii=False, indent=2)
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(text + "\n", encoding="utf-8")
    print(text)


if __name__ == "__main__":
    main()
