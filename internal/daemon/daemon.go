package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/fyang0507/sundial/internal/gitops"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/localtz"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/power"
	"github.com/fyang0507/sundial/internal/store"
)

// activeSchedule holds the runtime representation of a single schedule.
type activeSchedule struct {
	desired *model.DesiredState
	runtime *model.RuntimeState
	trigger model.Trigger
	// window is the resolved active-hours window (nil when the schedule has no
	// window, meaning always active). Rebuilt from desired.ActiveHours whenever
	// the schedule is (re)built in reconcile/add/refresh/reactivate.
	window *model.ActiveWindow
	mu     sync.Mutex // per-schedule mutex to prevent overlapping runs
}

// nextFire computes the schedule's next fire time strictly after `after`,
// clamped to its active-hours window. When the trigger's natural slot falls
// outside the window, the returned time is the next window opening and deferred
// is true — the single choke point that makes every trigger type obey active
// hours. A schedule with no window returns the raw trigger time and deferred is
// always false.
func (s *activeSchedule) nextFire(after time.Time) (fire time.Time, deferred bool) {
	raw := s.trigger.NextFireTime(after)
	return s.window.Clamp(raw)
}

// Daemon is the core scheduler runtime. It ties together triggers, stores,
// gitops, and IPC into a single process that manages schedule lifecycle.
type Daemon struct {
	cfg           *model.Config
	desiredStore  *store.DesiredStore
	runtimeStore  *store.RuntimeStore
	runLogStore   *store.RunLogStore
	settingsStore *store.SettingsStore
	gitOps        *gitops.GitOps
	ipcServer     *ipc.Server

	schedules map[string]*activeSchedule // protected by mu
	mu        sync.RWMutex

	// --- macOS pmset wake management (opt-in via cfg.Daemon.Wake.Enabled) ---
	//
	// powerRunner is injectable so tests drive a mock and never shell out to real
	// pmset/sudo. Production gets power.DefaultRunner() in New().
	powerRunner power.CommandRunner
	// wakeMu guards the wake-management state below. It is separate from mu so
	// updateWakeSchedule (which may shell out to pmset) never blocks schedule
	// reads/writes, and so we never hold mu across a pmset call.
	wakeMu sync.Mutex
	// managedWakeAt is the single pmset wake event the daemon currently owns
	// (zero = none). We persist it (see wake.go) so a daemon restart can cancel a
	// stale event it scheduled in a previous run before scheduling a new one.
	managedWakeAt time.Time
	// wakeDisabledWarned guards the "wake enabled but pmset/permission missing"
	// WARN so we log it once rather than on every run-loop tick.
	wakeDisabledWarned bool

	startedAt time.Time

	// --- machine local-timezone tracking (for follow-local active-hours) ---
	//
	// The daemon is long-lived, so time.Local (cached at process start) goes
	// stale if the host's timezone changes while running (e.g. a laptop travels
	// NYC -> SFO). We track the current zone here, refresh it from the OS on each
	// run-loop tick via localtz, and recompute follow-local active-hours windows
	// when it changes. Guarded by localZoneMu (separate from mu so a zone probe
	// never blocks schedule reads/writes).
	localZoneMu   sync.Mutex
	localZoneName string
	localZone     *time.Location

	// activeHours is the parsed daemon-wide firing window (nil = no window),
	// applied to every schedule that doesn't set IgnoreActiveHours. When its
	// timezone is empty it follows the machine local zone (see localZone). It is
	// runtime-mutable via the set_active_hours RPC and persisted in settingsStore,
	// so access is guarded by activeHoursMu (the pointer is swapped, never
	// mutated in place). Separate from mu so a read never blocks schedule ops.
	activeHoursMu sync.Mutex
	activeHours   *model.ActiveHours

	wake chan struct{} // signal to re-evaluate next fire
	quit chan struct{} // shutdown signal
	done chan struct{} // closed when daemon fully stopped
	wg   sync.WaitGroup
}

// New initializes a Daemon from the given config. It creates sub-components
// (stores, gitops) but does not start serving or scheduling.
func New(cfg *model.Config) (*Daemon, error) {
	if cfg.DataRepo == "" {
		return nil, fmt.Errorf("data_repo is required")
	}

	zoneName, zone := localtz.Load()

	settingsStore := store.NewSettingsStore(cfg.State.Path)

	// Load the daemon-wide active-hours window from local runtime settings (set
	// via `sundial set-active-hours`, persisted across restarts). A missing file
	// yields no window; a read error degrades to no window with a WARN.
	var activeHours *model.ActiveHours
	if settings, err := settingsStore.Read(); err != nil {
		log.Printf("WARN: failed to read settings, disabling active-hours window: %v", err)
	} else {
		activeHours = settings.ActiveHours
	}

	d := &Daemon{
		cfg:           cfg,
		desiredStore:  store.NewDesiredStore(cfg.DataRepo),
		runtimeStore:  store.NewRuntimeStore(cfg.State.Path),
		runLogStore:   store.NewRunLogStore(cfg.State.LogsPath),
		settingsStore: settingsStore,
		gitOps:        gitops.NewGitOps(cfg.DataRepo),
		powerRunner:   power.DefaultRunner(),
		schedules:     make(map[string]*activeSchedule),
		startedAt:     time.Now(),
		localZoneName: zoneName,
		localZone:     zone,
		activeHours:   activeHours,
		wake:          make(chan struct{}, 1),
		quit:          make(chan struct{}),
		done:          make(chan struct{}),
	}

	return d, nil
}

// currentLocalZone returns the daemon's currently-tracked machine local zone.
// It is refreshed from the OS by maybeReconcileTimezone on each run-loop tick,
// so follow-local active-hours windows always resolve against the host's
// present timezone rather than the (cached) time.Local captured at startup.
func (d *Daemon) currentLocalZone() *time.Location {
	d.localZoneMu.Lock()
	defer d.localZoneMu.Unlock()
	if d.localZone == nil {
		return time.Local
	}
	return d.localZone
}

// buildWindow resolves the EFFECTIVE active-hours window for a schedule: the
// daemon-wide window (DaemonConfig.ActiveHours), resolved against the current
// local zone for a follow-local window or its pinned zone. It returns nil —
// meaning always active — when there is no global window or the schedule opts
// out via IgnoreActiveHours. A malformed global window degrades to nil (config
// validation rejects it at startup, so this should be unreachable).
func (d *Daemon) buildWindow(ds *model.DesiredState) *model.ActiveWindow {
	ah := d.getActiveHours()
	if ah == nil || ds.IgnoreActiveHours {
		return nil
	}
	w, err := ah.WindowIn(d.currentLocalZone())
	if err != nil {
		log.Printf("WARN: schedule %s (%s): invalid active_hours %s, ignoring window: %v",
			ds.ID, ds.Name, ah.Describe(), err)
		return nil
	}
	return w
}

// getActiveHours returns the current daemon-wide active-hours window (nil = none)
// under activeHoursMu. setActiveHours swaps it. The returned *ActiveHours is
// never mutated in place, so callers may read it without holding the lock.
func (d *Daemon) getActiveHours() *model.ActiveHours {
	d.activeHoursMu.Lock()
	defer d.activeHoursMu.Unlock()
	return d.activeHours
}

func (d *Daemon) setActiveHours(ah *model.ActiveHours) {
	d.activeHoursMu.Lock()
	d.activeHours = ah
	d.activeHoursMu.Unlock()
}

// Start brings the daemon online:
//  1. Ensures store directories exist
//  2. Runs initial reconciliation (handling missed fires)
//  3. Creates and starts the IPC server
//  4. Starts the scheduler run loop
//  5. Sets up signal handling for graceful shutdown
func (d *Daemon) Start() error {
	// 1. Ensure store directories exist.
	if err := d.desiredStore.EnsureDir(); err != nil {
		return fmt.Errorf("ensure desired store dir: %w", err)
	}
	if err := d.runtimeStore.EnsureDir(); err != nil {
		return fmt.Errorf("ensure runtime store dir: %w", err)
	}
	if err := d.runLogStore.EnsureDir(); err != nil {
		return fmt.Errorf("ensure run log store dir: %w", err)
	}

	// 1b. Load any pmset wake event we persisted in a previous run so the first
	// updateWakeSchedule can cancel/replace a stale event rather than orphan it.
	// Best-effort: a missing/corrupt file just means "no event known".
	d.loadManagedWake()

	// 2. Run initial reconciliation with missed fire handling.
	if err := d.reconcile(true); err != nil {
		return fmt.Errorf("startup reconciliation: %w", err)
	}

	// 3. Create IPC server with daemon as the Handler.
	srv, err := ipc.NewServer(d.cfg.Daemon.SocketPath, d)
	if err != nil {
		return fmt.Errorf("create IPC server: %w", err)
	}
	d.ipcServer = srv
	d.ipcServer.Serve()

	// 4. Start scheduler run loop.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		d.runLoop()
	}()

	// 5. Signal handling.
	d.wg.Add(1)
	go func() {
		defer d.wg.Done()
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
		select {
		case sig := <-sigCh:
			log.Printf("received signal %s, shutting down", sig)
			d.Stop()
		case <-d.quit:
		}
		signal.Stop(sigCh)
	}()

	log.Printf("daemon started, socket=%s", d.cfg.Daemon.SocketPath)
	return nil
}

// Stop initiates a graceful shutdown: closes the quit channel, shuts down
// the IPC server, and waits for goroutines to exit.
func (d *Daemon) Stop() {
	select {
	case <-d.quit:
		// Already closed.
		return
	default:
		close(d.quit)
	}

	if d.ipcServer != nil {
		d.ipcServer.Shutdown()
	}

	d.wg.Wait()
	close(d.done)
	log.Printf("daemon stopped")
}

// Wait blocks until the daemon has fully stopped.
func (d *Daemon) Wait() {
	<-d.done
}

// signalWake sends a non-blocking signal on the wake channel to cause
// the run loop to re-evaluate the next fire time.
func (d *Daemon) signalWake() {
	select {
	case d.wake <- struct{}{}:
	default:
	}
}
