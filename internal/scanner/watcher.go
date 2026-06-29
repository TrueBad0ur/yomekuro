package scanner

import (
	"context"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/truebad0ur/yomekuro/internal/db"
)

type Watcher struct {
	fw       *fsnotify.Watcher
	sc       *Scanner
	pool     *pgxpool.Pool
	mu       sync.Mutex
	debounce map[string]*time.Timer
	libsMu   sync.RWMutex
	libs     []db.Library
}

func NewWatcher(sc *Scanner, pool *pgxpool.Pool) (*Watcher, error) {
	fw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		fw:       fw,
		sc:       sc,
		pool:     pool,
		debounce: make(map[string]*time.Timer),
	}, nil
}

// AddLibrary adds a library to the watch list and recursively watches its directories.
func (w *Watcher) AddLibrary(lib db.Library) {
	w.libsMu.Lock()
	for _, l := range w.libs {
		if l.ID == lib.ID {
			w.libsMu.Unlock()
			return
		}
	}
	w.libs = append(w.libs, lib)
	w.libsMu.Unlock()

	_ = filepath.WalkDir(lib.Path, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if err := w.fw.Add(path); err != nil {
			slog.Warn("watcher: add dir", "path", path, "err", err)
		}
		return nil
	})
}

// Start watches existing libraries and begins the event loop.
func (w *Watcher) Start(ctx context.Context, libs []db.Library) {
	for _, lib := range libs {
		w.AddLibrary(lib)
	}
	go w.loop(ctx)
}

func (w *Watcher) loop(ctx context.Context) {
	defer w.fw.Close()
	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-w.fw.Events:
			if !ok {
				return
			}
			w.handleEvent(ctx, event)
		case err, ok := <-w.fw.Errors:
			if !ok {
				return
			}
			slog.Warn("watcher error", "err", err)
		}
	}
}

func (w *Watcher) handleEvent(ctx context.Context, event fsnotify.Event) {
	name := event.Name

	// New directory → watch it recursively
	if event.Has(fsnotify.Create) {
		if fi, err := os.Stat(name); err == nil && fi.IsDir() {
			if err := w.fw.Add(name); err != nil {
				slog.Warn("watcher: add new dir", "path", name, "err", err)
			}
			return
		}
	}

	if !strings.EqualFold(filepath.Ext(name), ".epub") {
		return
	}

	if event.Has(fsnotify.Remove) || event.Has(fsnotify.Rename) {
		w.debounceOp(name, func() {
			if err := db.DeleteBookByPath(ctx, w.pool, name); err != nil {
				slog.Error("watcher: delete book", "path", name, "err", err)
			} else {
				slog.Info("watcher: removed book", "path", name)
			}
		})
		return
	}

	if event.Has(fsnotify.Create) || event.Has(fsnotify.Write) {
		lib, ok := w.findLibrary(name)
		if !ok {
			return
		}
		w.debounceOp(name, func() {
			updated, err := w.sc.processFile(ctx, lib, name)
			if err != nil {
				slog.Error("watcher: process file", "path", name, "err", err)
			} else if updated {
				slog.Info("watcher: added/updated book", "path", name)
			}
		})
	}
}

func (w *Watcher) findLibrary(filePath string) (db.Library, bool) {
	w.libsMu.RLock()
	defer w.libsMu.RUnlock()
	for _, lib := range w.libs {
		if strings.HasPrefix(filePath, lib.Path) {
			return lib, true
		}
	}
	return db.Library{}, false
}

func (w *Watcher) debounceOp(path string, fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if t, ok := w.debounce[path]; ok {
		t.Stop()
	}
	w.debounce[path] = time.AfterFunc(2*time.Second, func() {
		fn()
		w.mu.Lock()
		delete(w.debounce, path)
		w.mu.Unlock()
	})
}
