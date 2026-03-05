package scenario

import (
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

type Watcher struct {
	engine  *Engine
	path    string
	watcher *fsnotify.Watcher

	mu   sync.Mutex
	stop chan struct{}
}

func NewWatcher(engine *Engine, path string) *Watcher {
	return &Watcher{
		engine: engine,
		path:   path,
		stop:   make(chan struct{}),
	}
}

func (w *Watcher) Start() error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	dir := filepath.Dir(w.path)
	if err := watcher.Add(dir); err != nil {
		watcher.Close()
		return err
	}

	slog.Info("watching scenario config for changes", "path", w.path, "dir", dir)

	go w.loop()
	return nil
}

func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	select {
	case <-w.stop:
	default:
		close(w.stop)
	}
	if w.watcher != nil {
		w.watcher.Close()
	}
}

func (w *Watcher) loop() {
	base := filepath.Base(w.path)
	for {
		select {
		case <-w.stop:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			name := filepath.Base(event.Name)
			isTarget := name == base
			isSymlinkSwap := name == "..data" && event.Has(fsnotify.Create)

			if !isTarget && !isSymlinkSwap {
				continue
			}
			if isTarget && !(event.Has(fsnotify.Write) || event.Has(fsnotify.Create)) {
				continue
			}

			slog.Info("scenario config changed, reloading", "trigger", name, "event", event.Op.String())
			if err := w.engine.LoadFromFile(w.path); err != nil {
				slog.Error("failed to reload scenarios", "error", err)
			} else {
				slog.Info("scenarios reloaded successfully")
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Error("fsnotify error", "error", err)
		}
	}
}
