package storage

import (
        "database/sql"
        "encoding/json"
        "fmt"
        "os"
        "path/filepath"
        "time"

        _ "modernc.org/sqlite"
)

type Storage struct {
        db *sql.DB
}

func Open(dbPath string) (*Storage, error) {
        if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
                return nil, err
        }
        db, err := sql.Open("sqlite", dbPath)
        if err != nil {
                return nil, err
        }
        s := &Storage{db: db}
        if err := s.init(); err != nil {
                _ = db.Close()
                return nil, err
        }
        return s, nil
}

func (s *Storage) Close() error { return s.db.Close() }

func (s *Storage) init() error {
        _, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS signals (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts REAL NOT NULL,
  stock_id INTEGER,
  code TEXT,
  action TEXT,
  score REAL,
  confidence REAL,
  price REAL,
  reason TEXT,
  payload TEXT
);
CREATE TABLE IF NOT EXISTS trades (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts REAL NOT NULL,
  mode TEXT,
  stock_id INTEGER,
  code TEXT,
  side TEXT,
  shares REAL,
  price REAL,
  status TEXT,
  reason TEXT,
  raw TEXT
);
CREATE TABLE IF NOT EXISTS paper_state (
  id INTEGER PRIMARY KEY CHECK (id=1),
  cash REAL,
  updated_at REAL,
  positions_json TEXT
);
CREATE TABLE IF NOT EXISTS snapshots (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  ts REAL,
  kind TEXT,
  payload TEXT
);
CREATE TABLE IF NOT EXISTS runtime_state (
  key TEXT PRIMARY KEY,
  value TEXT,
  updated_at REAL
);
`)
        return err
}

func (s *Storage) SetState(key string, value any) error {
        b, err := json.Marshal(value)
        if err != nil {
                return err
        }
        _, err = s.db.Exec(
                `INSERT INTO runtime_state(key,value,updated_at) VALUES(?,?,?)
                 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
                key, string(b), float64(time.Now().UnixNano())/1e9,
        )
        return err
}

func (s *Storage) GetState(key string, dest any) error {
        var raw string
        err := s.db.QueryRow(`SELECT value FROM runtime_state WHERE key=?`, key).Scan(&raw)
        if err == sql.ErrNoRows {
                return nil
        }
        if err != nil {
                return err
        }
        if raw == "" || dest == nil {
                return nil
        }
        return json.Unmarshal([]byte(raw), dest)
}

func (s *Storage) GetStateMap(key string) map[string]any {
        out := map[string]any{}
        _ = s.GetState(key, &out)
        if out == nil {
                return map[string]any{}
        }
        return out
}

func (s *Storage) RecentTrades(limit int) []map[string]any {
        // Prefer Python schema columns; fallback to go-init schema if needed.
        rows, err := s.db.Query(`SELECT ts,mode,stock_id,code,side,shares,price,status,reason,raw FROM trades ORDER BY id DESC LIMIT ?`, limit)
        if err != nil {
                rows, err = s.db.Query(`SELECT ts,code,side,shares,price,status,reason,mode,payload FROM trades ORDER BY id DESC LIMIT ?`, limit)
                if err != nil {
                        return nil
                }
                defer rows.Close()
                var out []map[string]any
                for rows.Next() {
                        var ts sql.NullFloat64
                        var code, side, status, reason, mode, payload sql.NullString
                        var shares, price sql.NullFloat64
                        if err := rows.Scan(&ts, &code, &side, &shares, &price, &status, &reason, &mode, &payload); err != nil {
                                continue
                        }
                        item := map[string]any{
                                "ts": ts.Float64, "code": code.String, "side": side.String,
                                "shares": shares.Float64, "price": price.Float64, "status": status.String,
                                "reason": reason.String, "mode": mode.String,
                        }
                        if payload.Valid && payload.String != "" {
                                var p any
                                if json.Unmarshal([]byte(payload.String), &p) == nil {
                                        item["raw"] = p
                                }
                        }
                        out = append(out, item)
                }
                return out
        }
        defer rows.Close()
        var out []map[string]any
        for rows.Next() {
                var ts sql.NullFloat64
                var mode, code, side, status, reason, raw sql.NullString
                var stockID sql.NullInt64
                var shares, price sql.NullFloat64
                if err := rows.Scan(&ts, &mode, &stockID, &code, &side, &shares, &price, &status, &reason, &raw); err != nil {
                        continue
                }
                item := map[string]any{
                        "ts": ts.Float64, "mode": mode.String, "stock_id": stockID.Int64, "code": code.String,
                        "side": side.String, "shares": shares.Float64, "price": price.Float64,
                        "status": status.String, "reason": reason.String,
                }
                if raw.Valid && raw.String != "" {
                        var p any
                        if json.Unmarshal([]byte(raw.String), &p) == nil {
                                item["raw"] = p
                        }
                }
                out = append(out, item)
        }
        return out
}

func (s *Storage) TradeStats() map[string]any {
        var total, okN, failN int
        _ = s.db.QueryRow(`SELECT COUNT(1) FROM trades`).Scan(&total)
        _ = s.db.QueryRow(`SELECT COUNT(1) FROM trades WHERE lower(status) IN ('submitted','filled','ok','success')`).Scan(&okN)
        _ = s.db.QueryRow(`SELECT COUNT(1) FROM trades WHERE lower(status) IN ('error','failed','fail')`).Scan(&failN)
        return map[string]any{"total": total, "ok": okN, "failed": failN}
}

func (s *Storage) RecentSnapshots(kind string, limit int) []map[string]any {
        q := `SELECT ts,kind,payload FROM snapshots`
        args := []any{}
        if kind != "" {
                q += ` WHERE kind=?`
                args = append(args, kind)
        }
        q += ` ORDER BY id DESC LIMIT ?`
        args = append(args, limit)
        rows, err := s.db.Query(q, args...)
        if err != nil {
                return nil
        }
        defer rows.Close()
        var out []map[string]any
        for rows.Next() {
                var ts sql.NullFloat64
                var k, payload sql.NullString
                if err := rows.Scan(&ts, &k, &payload); err != nil {
                        continue
                }
                item := map[string]any{"ts": ts.Float64, "kind": k.String}
                if payload.Valid && payload.String != "" {
                        var p any
                        if json.Unmarshal([]byte(payload.String), &p) == nil {
                                item["payload"] = p
                        } else {
                                item["payload"] = payload.String
                        }
                }
                out = append(out, item)
        }
        return out
}

func MustOpen(dbPath string) *Storage {
        s, err := Open(dbPath)
        if err != nil {
                panic(fmt.Errorf("open storage: %w", err))
        }
        return s
}


func (s *Storage) LogSignal(signal map[string]any) error {
        b, _ := json.Marshal(signal)
        _, err := s.db.Exec(
                `INSERT INTO signals(ts,stock_id,code,action,score,confidence,price,reason,payload) VALUES(?,?,?,?,?,?,?,?,?)`,
                float64(time.Now().UnixNano())/1e9,
                int(asFloatLocal(signal["stock_id"])),
                fmt.Sprint(signal["code"]),
                fmt.Sprint(signal["action"]),
                asFloatLocal(signal["score"]),
                asFloatLocal(signal["confidence"]),
                asFloatLocal(signal["price"]),
                fmt.Sprint(signal["reason"]),
                string(b),
        )
        return err
}

func (s *Storage) LogTrade(trade map[string]any) error {
        raw, _ := json.Marshal(trade["raw"])
        _, err := s.db.Exec(
                `INSERT INTO trades(ts,mode,stock_id,code,side,shares,price,status,reason,raw) VALUES(?,?,?,?,?,?,?,?,?,?)`,
                float64(time.Now().UnixNano())/1e9,
                fmt.Sprint(trade["mode"]),
                int(asFloatLocal(trade["stock_id"])),
                fmt.Sprint(trade["code"]),
                fmt.Sprint(trade["side"]),
                asFloatLocal(trade["shares"]),
                asFloatLocal(trade["price"]),
                fmt.Sprint(trade["status"]),
                fmt.Sprint(trade["reason"]),
                string(raw),
        )
        return err
}

func (s *Storage) Snapshot(kind string, payload any) error {
        b, err := json.Marshal(payload)
        if err != nil {
                return err
        }
        _, err = s.db.Exec(`INSERT INTO snapshots(ts,kind,payload) VALUES(?,?,?)`, float64(time.Now().UnixNano())/1e9, kind, string(b))
        return err
}

func asFloatLocal(v any) float64 {
        switch t := v.(type) {
        case float64:
                return t
        case float32:
                return float64(t)
        case int:
                return float64(t)
        case int64:
                return float64(t)
        default:
                return 0
        }
}

func (s *Storage) LoadPaper() (cash float64, positions []map[string]any, ok bool) {
        // Prefer Python schema columns.
        var c sql.NullFloat64
        var pj, ptext sql.NullString
        err := s.db.QueryRow(`SELECT cash, positions_json FROM paper_state WHERE id=1`).Scan(&c, &pj)
        if err != nil {
                // fallback go-init schema
                err = s.db.QueryRow(`SELECT cash, positions FROM paper_state WHERE id=1`).Scan(&c, &ptext)
                if err != nil {
                        return 0, nil, false
                }
                pj = ptext
        }
        positions = []map[string]any{}
        raw := ""
        if pj.Valid {
                raw = pj.String
        }
        if raw != "" {
                var arr []any
                if json.Unmarshal([]byte(raw), &arr) == nil {
                        for _, it := range arr {
                                if m, ok := it.(map[string]any); ok {
                                        positions = append(positions, m)
                                }
                        }
                } else {
                        var arr2 []map[string]any
                        if json.Unmarshal([]byte(raw), &arr2) == nil {
                                positions = arr2
                        }
                }
        }
        return c.Float64, positions, true
}

func (s *Storage) SavePaper(cash float64, positions []map[string]any) error {
        b, err := json.Marshal(positions)
        if err != nil {
                return err
        }
        // Write both Python and go-compatible columns when present.
        _, err = s.db.Exec(
                `INSERT INTO paper_state(id, cash, updated_at, positions_json)
                 VALUES(1,?,?,?)
                 ON CONFLICT(id) DO UPDATE SET
                   cash=excluded.cash,
                   updated_at=excluded.updated_at,
                   positions_json=excluded.positions_json`,
                cash, float64(time.Now().UnixNano())/1e9, string(b),
        )
        if err != nil {
                // fallback older schema
                _, err = s.db.Exec(
                        `INSERT INTO paper_state(id, cash, positions) VALUES(1,?,?)
                         ON CONFLICT(id) DO UPDATE SET cash=excluded.cash, positions=excluded.positions`,
                        cash, string(b),
                )
                if err != nil {
                        _, err = s.db.Exec(
                                `INSERT INTO paper_state(id, cash, positions_json) VALUES(1,?,?)
                                 ON CONFLICT(id) DO UPDATE SET cash=excluded.cash, positions_json=excluded.positions_json`,
                                cash, string(b),
                        )
                }
        }
        return err
}
