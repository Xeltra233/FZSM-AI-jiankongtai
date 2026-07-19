package dashboard

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"
)

// handleDBExport streams a consistent snapshot of the live SQLite DB as a
// downloadable file. Admin-gated (also covered by withAdminAPIGate) because the
// database contains account/runtime data. Uses VACUUM INTO so the export is
// transactionally consistent even while the bot is trading.
func (s *Server) handleDBExport(w http.ResponseWriter, r *http.Request) {
	if !requireAdmin(w, r) {
		return
	}
	if r.Method != http.MethodGet {
		writeJSON(w, 405, map[string]any{"ok": false, "error": "方法不允许"})
		return
	}

	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("fzsm_db_export_%d.db", time.Now().UnixNano()))
	if err := s.st.SnapshotTo(tmp); err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "数据库快照失败"})
		return
	}
	_ = os.Chmod(tmp, 0o600)
	defer os.Remove(tmp)

	f, err := os.Open(tmp)
	if err != nil {
		writeJSON(w, 500, map[string]any{"ok": false, "error": err.Error(), "message": "打开快照失败"})
		return
	}
	defer f.Close()

	fname := fmt.Sprintf("fzsm_bot_%s.db", time.Now().Format("20060102_150405"))
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", fname))
	w.Header().Set("Cache-Control", "no-store")
	if info, err := f.Stat(); err == nil {
		w.Header().Set("Content-Length", strconv.FormatInt(info.Size(), 10))
	}
	_, _ = io.Copy(w, f)
}
