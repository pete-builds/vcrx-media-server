package library

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"sync"
	"time"
)

// Manager runs scans: at most one per library at a time, on demand or on each
// library's schedule.
type Manager struct {
	s   *Scanner
	log *slog.Logger

	// InitialDelay is how long after Schedule starts the first pass begins, and
	// Stagger spaces libraries apart so they do not hit the NAS together. Tick
	// is how often the schedule is checked. Tests shorten them.
	InitialDelay time.Duration
	Stagger      time.Duration
	Tick         time.Duration

	mu      sync.Mutex
	running map[int64]bool
	wg      sync.WaitGroup
	base    context.Context
	stop    context.CancelFunc
}

// NewManager wraps a scanner. Call Shutdown to stop scans started with Start.
func NewManager(s *Scanner, log *slog.Logger) *Manager {
	if log == nil {
		log = s.log()
	}
	base, stop := context.WithCancel(context.Background())
	return &Manager{s: s, log: log, InitialDelay: 30 * time.Second, Stagger: 15 * time.Second, Tick: 15 * time.Second,
		running: map[int64]bool{}, base: base, stop: stop}
}

func (m *Manager) acquire(id int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.running[id] {
		return false
	}
	m.running[id] = true
	return true
}

func (m *Manager) release(id int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.running, id)
}

// Running reports whether this process is scanning the library now.
func (m *Manager) Running(libraryID int64) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.running[libraryID]
}

func (m *Manager) load(ctx context.Context, id int64) (Library, error) {
	var lib Library
	err := m.s.DB.QueryRowContext(ctx, `SELECT id, name, kind, root_path, grouping FROM libraries WHERE id = ?`, id).
		Scan(&lib.ID, &lib.Name, &lib.Kind, &lib.RootPath, &lib.Grouping)
	if errors.Is(err, sql.ErrNoRows) {
		return lib, ErrNoLibrary
	}
	return lib, err
}

// Start begins a scan in the background and returns its run id once the run
// row exists. The scan outlives ctx (an HTTP request, usually); Shutdown
// cancels it.
func (m *Manager) Start(ctx context.Context, libraryID int64, opt Options) (int64, error) {
	lib, err := m.load(ctx, libraryID)
	if err != nil {
		return 0, err
	}
	if !m.acquire(lib.ID) {
		return 0, ErrScanRunning
	}
	run, err := m.s.begin(ctx, lib, opt)
	if err != nil {
		m.release(lib.ID)
		return 0, err
	}
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer m.release(lib.ID)
		if _, err := m.s.execute(m.base, lib, opt, run); err != nil {
			m.log.Warn("scan did not complete", "library", lib.Name, "run", run.ID, "err", err)
		}
	}()
	return run.ID, nil
}

// RunNow scans and waits, for the CLI.
func (m *Manager) RunNow(ctx context.Context, libraryID int64, opt Options) (Run, error) {
	if opt.Trigger == "" {
		opt.Trigger = "cli"
	}
	lib, err := m.load(ctx, libraryID)
	if err != nil {
		return Run{}, err
	}
	if !m.acquire(lib.ID) {
		return Run{}, ErrScanRunning
	}
	defer m.release(lib.ID)
	return m.s.Scan(ctx, lib, opt)
}

// Shutdown cancels background scans and waits for them to close their runs.
func (m *Manager) Shutdown(ctx context.Context) error {
	m.stop()
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Schedule scans every library every scan_interval_minutes until ctx ends. The
// first pass starts InitialDelay after the call, libraries Stagger apart.
// Scheduled scans run one at a time; a library with an interval of 0 or less
// is never scanned on a schedule.
func (m *Manager) Schedule(ctx context.Context) {
	start := m.s.now()
	next := map[int64]time.Time{}
	first := map[int64]bool{}
	tick := m.Tick
	if tick <= 0 {
		tick = 15 * time.Second
	}
	t := time.NewTicker(tick)
	defer t.Stop()
	for {
		libs, err := m.schedule(ctx)
		if err != nil && ctx.Err() == nil {
			m.log.Error("load libraries for the schedule", "err", err)
		}
		for i, l := range libs {
			if l.interval <= 0 {
				continue
			}
			if _, ok := next[l.lib.ID]; !ok {
				next[l.lib.ID] = start.Add(m.InitialDelay + time.Duration(i)*m.Stagger)
				first[l.lib.ID] = true
			}
			if ctx.Err() != nil {
				return
			}
			if m.s.now().Before(next[l.lib.ID]) {
				continue
			}
			trigger := "schedule"
			if first[l.lib.ID] {
				trigger = "startup"
			}
			if !m.acquire(l.lib.ID) {
				continue
			}
			_, err := m.s.Scan(ctx, l.lib, Options{Trigger: trigger})
			m.release(l.lib.ID)
			if errors.Is(err, ErrScanRunning) {
				continue
			}
			if err != nil && ctx.Err() == nil {
				m.log.Warn("scheduled scan did not complete", "library", l.lib.Name, "err", err)
			}
			first[l.lib.ID] = false
			next[l.lib.ID] = m.s.now().Add(time.Duration(l.interval) * time.Minute)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

type scheduled struct {
	lib      Library
	interval int
}

func (m *Manager) schedule(ctx context.Context) ([]scheduled, error) {
	rows, err := m.s.DB.QueryContext(ctx, `SELECT id, name, kind, root_path, grouping, scan_interval_minutes FROM libraries ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []scheduled
	for rows.Next() {
		var s scheduled
		if err := rows.Scan(&s.lib.ID, &s.lib.Name, &s.lib.Kind, &s.lib.RootPath, &s.lib.Grouping, &s.interval); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
