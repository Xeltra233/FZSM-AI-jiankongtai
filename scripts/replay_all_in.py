#!/usr/bin/env python3
"""用云端数据库末态和真实买入信号离线比较旧/新 all-in 资金部署速度。"""

from __future__ import annotations

import argparse
import json
import sqlite3
from pathlib import Path
from typing import Any


def load(raw: str) -> dict[str, Any]:
    try:
        value = json.loads(raw)
        return value if isinstance(value, dict) else {}
    except (TypeError, ValueError):
        return {}


def latest_account(con: sqlite3.Connection) -> dict[str, Any]:
    for (raw,) in con.execute("SELECT payload FROM snapshots ORDER BY id DESC"):
        account = load(raw).get("account")
        if isinstance(account, dict) and float(account.get("equity") or 0) > 0:
            return account
    raise RuntimeError("no account snapshot")


def candidates(con: sqlite3.Connection, limit: int = 10) -> list[dict[str, float | str]]:
    out, seen = [], set()
    rows = con.execute(
        "SELECT code,price,score,payload FROM signals WHERE action='buy' ORDER BY id DESC LIMIT 10000"
    )
    for code, price, score, raw in rows:
        if code in seen or float(price or 0) <= 0:
            continue
        payload = load(raw)
        ev = payload.get("trade_ev") if isinstance(payload.get("trade_ev"), dict) else {}
        edge = float(ev.get("net_edge") or 0)
        target = float(ev.get("target_position_pct") or 0)
        if edge <= 0 or target <= 0:
            continue
        seen.add(code)
        out.append({"code": code, "price": float(price), "score": float(score or 0), "edge": edge, "target_pct": target})
        if len(out) >= limit:
            break
    return sorted(out, key=lambda x: (-float(x["edge"]), -float(x["score"])))


def simulate(equity: float, cash: float, rows: list[dict[str, Any]], optimized: bool, cycles: int) -> dict[str, Any]:
    reserve = equity * 0.02
    holdings: dict[str, float] = {}
    actions = 0
    for _ in range(cycles):
        per_cycle = 3 if optimized else 1
        cycle_actions = 0
        for row in rows:
            if cycle_actions >= per_cycle or cash <= reserve:
                break
            code = str(row["code"])
            existing = code in holdings
            max_positions = 10 if optimized else 6
            if not existing and len(holdings) >= max_positions:
                continue
            # Old implementation blocked every add after max_positions was reached.
            if not optimized and len(holdings) >= max_positions:
                continue
            target_pct = min(float(row["target_pct"]) * 1.25, 0.35)
            position_cap = equity * (0.30 if optimized else 0.22)
            gap = max(position_cap - holdings.get(code, 0), 0)
            order_cap = min(max(500000.0, equity * 0.08), 10000000.0) if optimized else 500000.0
            budget = min(equity * target_pct, cash - reserve, gap, order_cap)
            if budget < float(row["price"]):
                continue
            holdings[code] = holdings.get(code, 0) + budget
            cash -= budget
            actions += 1
            cycle_actions += 1
    invested = equity - cash
    return {
        "cycles": cycles,
        "actions": actions,
        "positions": len(holdings),
        "cash": round(cash, 2),
        "invested": round(invested, 2),
        "cash_pct": cash / equity,
        "invested_pct": invested / equity,
    }


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("db", type=Path)
    ap.add_argument("--cycles", type=int, default=30)
    ap.add_argument("--out", type=Path)
    args = ap.parse_args()
    con = sqlite3.connect(f"file:{args.db.resolve().as_posix()}?mode=ro", uri=True)
    account = latest_account(con)
    equity, cash = float(account["equity"]), float(account["cash"])
    rows = candidates(con)
    report = {
        "source": args.db.name,
        "mode": "offline_policy_replay",
        "assumptions": {
            "signals": "latest unique positive-edge buy signals from cloud DB",
            "reserve_pct": 0.02,
            "price_and_edge_static": True,
            "fills": "policy capacity comparison; not a profit forecast",
        },
        "initial": {"equity": equity, "cash": cash, "cash_pct": cash / equity, "candidate_count": len(rows)},
        "baseline": simulate(equity, cash, rows, False, args.cycles),
        "optimized": simulate(equity, cash, rows, True, args.cycles),
        "candidates": rows,
    }
    report["improvement"] = {
        "extra_invested": round(report["optimized"]["invested"] - report["baseline"]["invested"], 2),
        "cash_pct_reduction": report["baseline"]["cash_pct"] - report["optimized"]["cash_pct"],
    }
    text = json.dumps(report, ensure_ascii=False, indent=2)
    if args.out:
        args.out.parent.mkdir(parents=True, exist_ok=True)
        args.out.write_text(text + "\n", encoding="utf-8")
    print(text)


if __name__ == "__main__":
    main()
