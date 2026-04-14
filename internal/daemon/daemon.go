package daemon

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/fyang0507/sundial/internal/gitops"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/store"
)

// activeSchedule holds the runtime representation of a single schedule.
type activeSchedule struct {
	desired *model.DesiredState
	runtime *model.RuntimeState
	trigger model.Trigger
	mu      sync.Mutex // per-schedule mutex to prevent overlapping runs
}

// Daemon is the core scheduler runtime. It ties together triggers, stores,
// gitops, and IPC into a single process that manages schedule lifecycle.
type Daemon struct {
	cfg          *model.Config
	desiredStore *store.DesiredStore
	runtimeStore *store.RuntimeStore
	runLogStore  *store.RunLogStore
	gitOps       *gitops.GitOps
	ipcServer    *ipc.Server

	schedules map[string]*activeSchedule // protected by mu
	mu        sync.RWMutex

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

	d := &Daemon{
		cfg:          cfg,
		desiredStore: store.NewDesiredStore(cfg.DataRepo),
		runtimeStore: store.NewRuntimeStore(cfg.State.Path),
		runLogStore:  store.NewRunLogStore(cfg.State.LogsPath),
		gitOps:       gitops.NewGitOps(cfg.DataRepo),
		schedules:    make(map[string]*activeSchedule),
		wake:         make(chan struct{}, 1),
		quit:         make(chan struct{}),
		done:         make(chan struct{}),
	}

	return d, nil
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
