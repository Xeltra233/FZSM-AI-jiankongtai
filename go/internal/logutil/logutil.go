package logutil

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Options controls file logging + cleanup.
type Options struct {
	Dir        string
	Name       string // base file name, e.g. bot.log / dashboard.log
	MaxSizeMB  int    // rotate when larger than this
	MaxBackups int    // keep at most N rotated files for this name
	MaxAgeDays int    // delete files older than N days in log dir
	AlsoStdout bool   // mirror to stdout/stderr
}

type rotateWriter struct {
	mu       sync.Mutex
	opts     Options
	path     string
	file     *os.File
	size     int64
	maxBytes int64
}

// Setup configures standard library log to a rotating file under opts.Dir.
// Returns a closer that should be deferred.
func Setup(opts Options) (io.Closer, error) {
	opts = withDefaults(opts)
	if err := os.MkdirAll(opts.Dir, 0o755); err != nil {
		return nil, err
	}
	// cleanup once on startup
	_ = Cleanup(opts.Dir, opts.Name, opts.MaxBackups, opts.MaxAgeDays)

	w, err := newRotateWriter(opts)
	if err != nil {
		return nil, err
	}
	var out io.Writer = w
	if opts.AlsoStdout {
		out = io.MultiWriter(os.Stdout, w)
	}
	log.SetOutput(out)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("logutil ready dir=%s name=%s max_size_mb=%d max_backups=%d max_age_days=%d",
		opts.Dir, opts.Name, opts.MaxSizeMB, opts.MaxBackups, opts.MaxAgeDays)

	// periodic cleanup every hour
	stop := make(chan struct{})
	go func() {
		t := time.NewTicker(1 * time.Hour)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				_ = Cleanup(opts.Dir, opts.Name, opts.MaxBackups, opts.MaxAgeDays)
			case <-stop:
				return
			}
		}
	}()

	return closerFunc(func() error {
		close(stop)
		return w.Close()
	}), nil
}

type closerFunc func() error

func (f closerFunc) Close() error { return f() }

func withDefaults(o Options) Options {
	if strings.TrimSpace(o.Dir) == "" {
		o.Dir = "logs"
	}
	if strings.TrimSpace(o.Name) == "" {
		o.Name = "app.log"
	}
	if o.MaxSizeMB <= 0 {
		o.MaxSizeMB = 50
	}
	if o.MaxBackups <= 0 {
		o.MaxBackups = 7
	}
	if o.MaxAgeDays <= 0 {
		o.MaxAgeDays = 7
	}
	return o
}

func newRotateWriter(opts Options) (*rotateWriter, error) {
	path := filepath.Join(opts.Dir, opts.Name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return &rotateWriter{
		opts:     opts,
		path:     path,
		file:     f,
		size:     st.Size(),
		maxBytes: int64(opts.MaxSizeMB) * 1024 * 1024,
	}, nil
}

func (w *rotateWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return 0, fmt.Errorf("log file closed")
	}
	if w.size+int64(len(p)) >= w.maxBytes {
		if err := w.rotateLocked(); err != nil {
			// still try write to current file
			n, werr := w.file.Write(p)
			w.size += int64(n)
			if werr != nil {
				return n, werr
			}
			return n, err
		}
	}
	n, err := w.file.Write(p)
	w.size += int64(n)
	return n, err
}

func (w *rotateWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return nil
	}
	err := w.file.Close()
	w.file = nil
	return err
}

func (w *rotateWriter) rotateLocked() error {
	if w.file != nil {
		_ = w.file.Close()
		w.file = nil
	}
	ts := time.Now().Format("20060102-150405")
	backup := fmt.Sprintf("%s.%s", w.path, ts)
	if err := os.Rename(w.path, backup); err != nil && !os.IsNotExist(err) {
		// if rename fails, reopen original and continue
		f, oerr := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if oerr != nil {
			return err
		}
		w.file = f
		return err
	}
	f, err := os.OpenFile(w.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	w.file = f
	w.size = 0
	_ = Cleanup(w.opts.Dir, w.opts.Name, w.opts.MaxBackups, w.opts.MaxAgeDays)
	return nil
}

// Cleanup removes old rotated logs by count and age.
// Current active file (exact name) is never deleted.
func Cleanup(dir, name string, maxBackups, maxAgeDays int) error {
	if strings.TrimSpace(dir) == "" {
		return nil
	}
	if maxBackups <= 0 {
		maxBackups = 7
	}
	if maxAgeDays <= 0 {
		maxAgeDays = 7
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	cutoff := time.Now().Add(-time.Duration(maxAgeDays) * 24 * time.Hour)
	type item struct {
		path string
		mod  time.Time
	}
	var rotated []item
	prefix := name + "."
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, e.Name())
		// age cleanup for common log files
		if info.ModTime().Before(cutoff) {
			// never delete active configured log name
			if e.Name() == name {
				continue
			}
			// only cleanup log-like files
			ln := strings.ToLower(e.Name())
			if strings.HasSuffix(ln, ".log") || strings.Contains(e.Name(), name+".") ||
				strings.HasSuffix(ln, ".out.log") || strings.HasSuffix(ln, ".err.log") ||
				strings.Contains(ln, ".log.") {
				_ = os.Remove(full)
				continue
			}
		}
		if strings.HasPrefix(e.Name(), prefix) {
			rotated = append(rotated, item{path: full, mod: info.ModTime()})
		}
	}
	if len(rotated) <= maxBackups {
		return nil
	}
	sort.Slice(rotated, func(i, j int) bool {
		return rotated[i].mod.After(rotated[j].mod) // newest first
	})
	for _, it := range rotated[maxBackups:] {
		_ = os.Remove(it.path)
	}
	return nil
}
