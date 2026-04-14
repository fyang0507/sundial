package integration

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fyang0507/sundial/internal/daemon"
	"github.com/fyang0507/sundial/internal/ipc"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/store"
	"github.com/fyang0507/sundial/internal/trigger"
)

// testEnv holds all components for a single integration test.
type testEnv struct {
	daemon  *daemon.Daemon
	client  *ipc.Client
	cfg     *model.Config
	dataDir string
}

// randomHex returns n random hex characters.
func randomHex(n int) string {
	b := make([]byte, (n+1)/2)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)[:n]
}

// setupTestDaemon creates a fully wired daemon with temp directories and returns
// a testEnv. The daemon is started and the IPC client is connected.
// Cleanup is registered via t.Cleanup.
func setupTestDaemon(t *testing.T) *testEnv {
	t.Helper()

	// 1. Create temp dir for data repo and init as git repo.
	dataDir, err := os.MkdirTemp("", "sundial-test-data-*")
	if err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	initGitRepo(t, dataDir)

	// 2. Create temp dir for runtime state.
	stateDir, err := os.MkdirTemp("", "sundial-test-state-*")
	if err != nil {
		t.Fatalf("create state dir: %v", err)
	}

	// 3. Create temp dir for run logs.
	logsDir, err := os.MkdirTemp("", "sundial-test-logs-*")
	if err != nil {
		t.Fatalf("create logs dir: %v", err)
	}

	// 4. Short socket path (Unix sockets have ~104 char limit).
	socketPath := fmt.Sprintf("/tmp/sundial-test-%s.sock", randomHex(8))

	// 5. Build config.
	cfg := &model.Config{
		DataRepo: dataDir,
		Daemon: model.DaemonConfig{
			SocketPath: socketPath,
			LogLevel:   "info",
			LogFile:    "", // no log file needed for tests
		},
		State: model.StateConfig{
			Path:     stateDir,
			LogsPath: logsDir,
		},
	}

	// 6. Create and start daemon.
	d, err := daemon.New(cfg)
	if err != nil {
		t.Fatalf("create daemon: %v", err)
	}
	if err := d.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}

	// 7. Create IPC client.
	client := ipc.NewClient(socketPath)

	// Wait briefly for the socket to be ready.
	waitForSocket(t, client)

	env := &testEnv{
		daemon:  d,
		client:  client,
		cfg:     cfg,
		dataDir: dataDir,
	}

	// 8. Register cleanup.
	t.Cleanup(func() {
		d.Stop()
		os.Remove(socketPath)
		os.RemoveAll(dataDir)
		os.RemoveAll(stateDir)
		os.RemoveAll(logsDir)
	})

	return env
}

// setupTestDaemonNoStart is like setupTestDaemon but does NOT start the daemon.
// The caller must call daemon.Start() manually after pre-populating state.
func setupTestDaemonNoStart(t *testing.T) *testEnv {
	t.Helper()

	dataDir, err := os.MkdirTemp("", "sundial-test-data-*")
	if err != nil {
		t.Fatalf("create data dir: %v", err)
	}
	initGitRepo(t, dataDir)

	stateDir, err := os.MkdirTemp("", "sundial-test-state-*")
	if err != nil {
		t.Fatalf("create state dir: %v", err)
	}

	logsDir, err := os.MkdirTemp("", "sundial-test-logs-*")
	if err != nil {
		t.Fatalf("create logs dir: %v", err)
	}

	socketPath := fmt.Sprintf("/tmp/sundial-test-%s.sock", randomHex(8))

	cfg := &model.Config{
		DataRepo: dataDir,
		Daemon: model.DaemonConfig{
			SocketPath: socketPath,
			LogLevel:   "info",
		},
		State: model.StateConfig{
			Path:     stateDir,
			LogsPath: logsDir,
		},
	}

	d, err := daemon.New(cfg)
	if err != nil {
		t.Fatalf("create daemon: %v", err)
	}

	env := &testEnv{
		daemon:  d,
		client:  ipc.NewClient(socketPath),
		cfg:     cfg,
		dataDir: dataDir,
	}

	t.Cleanup(func() {
		d.Stop()
		os.Remove(socketPath)
		os.RemoveAll(dataDir)
		os.RemoveAll(stateDir)
		os.RemoveAll(logsDir)
	})

	return env
}

// initGitRepo initializes a git repo with an initial commit.
func initGitRepo(t *testing.T, dir string) {
	t.Helper()
	cmds := [][]string{
		{"git", "init"},
		{"git", "config", "user.email", "test@sundial.dev"},
		{"git", "config", "user.name", "Sundial Test"},
		{"git", "commit", "--allow-empty", "-m", "init"},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git command %v failed: %v\n%s", args, err, out)
		}
	}
}

// waitForSocket polls until the daemon socket is reachable (up to 2 seconds).
func waitForSocket(t *testing.T, client *ipc.Client) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if err := client.Ping(); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for daemon socket to become available")
}

// addTestSchedule sends an add RPC with a unique cron schedule.
func addTestSchedule(t *testing.T, env *testEnv, name, command string) *model.AddResult {
	t.Helper()
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 9 * * 1-5",
		Command: command,
		Name:    name,
	}
	var result model.AddResult
	if err := env.client.Call(model.MethodAdd, params, &result); err != nil {
		t.Fatalf("add schedule %q: %v", name, err)
	}
	return &result
}

// ---------- Tests ----------

func TestAddAndList(t *testing.T) {
	env := setupTestDaemon(t)

	// Add a schedule.
	addResult := addTestSchedule(t, env, "Morning Job", "echo good morning")

	if addResult.ID == "" {
		t.Fatal("expected non-empty schedule ID")
	}
	if addResult.Name != "Morning Job" {
		t.Errorf("expected name %q, got %q", "Morning Job", addResult.Name)
	}
	if addResult.Status != "active" {
		t.Errorf("expected status 'active', got %q", addResult.Status)
	}

	// List schedules.
	var listResult model.ListResult
	if err := env.client.Call(model.MethodList, nil, &listResult); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listResult.Schedules) != 1 {
		t.Fatalf("expected 1 schedule in list, got %d", len(listResult.Schedules))
	}

	sched := listResult.Schedules[0]
	if sched.ID != addResult.ID {
		t.Errorf("list ID mismatch: expected %q, got %q", addResult.ID, sched.ID)
	}
	if sched.Name != "Morning Job" {
		t.Errorf("expected name %q, got %q", "Morning Job", sched.Name)
	}
	if sched.Status != "active" {
		t.Errorf("expected status 'active', got %q", sched.Status)
	}
}

func TestAddAndShow(t *testing.T) {
	env := setupTestDaemon(t)

	addResult := addTestSchedule(t, env, "Show Test", "echo show-test")

	// Show the schedule.
	var showResult model.ShowResult
	if err := env.client.Call(model.MethodShow, model.ShowParams{ID: addResult.ID}, &showResult); err != nil {
		t.Fatalf("show: %v", err)
	}

	if showResult.ID != addResult.ID {
		t.Errorf("ID mismatch: expected %q, got %q", addResult.ID, showResult.ID)
	}
	if showResult.Name != "Show Test" {
		t.Errorf("name mismatch: expected %q, got %q", "Show Test", showResult.Name)
	}
	if showResult.Command != "echo show-test" {
		t.Errorf("command mismatch: expected %q, got %q", "echo show-test", showResult.Command)
	}
	if showResult.Schedule == "" {
		t.Error("expected non-empty schedule description")
	}
	if showResult.NextFire == "" {
		t.Error("expected non-empty next_fire")
	}
	if showResult.Status != "active" {
		t.Errorf("expected status 'active', got %q", showResult.Status)
	}
	if showResult.CreatedAt == "" {
		t.Error("expected non-empty created_at")
	}
}

func TestAddAndRemove(t *testing.T) {
	env := setupTestDaemon(t)

	addResult := addTestSchedule(t, env, "Remove Test", "echo remove-me")

	// Verify it appears in list.
	var listResult model.ListResult
	if err := env.client.Call(model.MethodList, nil, &listResult); err != nil {
		t.Fatalf("list before remove: %v", err)
	}
	if len(listResult.Schedules) != 1 {
		t.Fatalf("expected 1 schedule before remove, got %d", len(listResult.Schedules))
	}

	// Remove it.
	var removeResult model.RemoveResult
	if err := env.client.Call(model.MethodRemove, model.RemoveParams{ID: addResult.ID}, &removeResult); err != nil {
		t.Fatalf("remove: %v", err)
	}
	if removeResult.Removed != 1 {
		t.Errorf("expected removed=1, got %d", removeResult.Removed)
	}

	// Verify list is empty.
	if err := env.client.Call(model.MethodList, nil, &listResult); err != nil {
		t.Fatalf("list after remove: %v", err)
	}
	if len(listResult.Schedules) != 0 {
		t.Errorf("expected 0 schedules after remove, got %d", len(listResult.Schedules))
	}
}

func TestAddDuplicateName(t *testing.T) {
	env := setupTestDaemon(t)

	// Add a schedule.
	addTestSchedule(t, env, "Unique Name", "echo first")

	// Try to add another with the same name.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * 1-5",
		Command: "echo second",
		Name:    "Unique Name",
	}
	var result model.AddResult
	err := env.client.Call(model.MethodAdd, params, &result)
	if err == nil {
		t.Fatal("expected error for duplicate name, got nil")
	}

	if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "exact_name") {
		t.Errorf("expected duplicate/exact_name error, got: %v", err)
	}

	// Add with Force=true should succeed.
	params.Force = true
	var forceResult model.AddResult
	if err := env.client.Call(model.MethodAdd, params, &forceResult); err != nil {
		t.Fatalf("force add should succeed: %v", err)
	}
	if forceResult.ID == "" {
		t.Error("expected non-empty ID from force add")
	}
}

func TestAddDuplicateCommand(t *testing.T) {
	env := setupTestDaemon(t)

	// Add a schedule with a specific command.
	addTestSchedule(t, env, "First Cmd", "echo duplicate-command-test")

	// Try to add another with a different name but same command.
	params := model.AddParams{
		Type:    model.TriggerTypeCron,
		Cron:    "0 10 * * 1-5",
		Command: "echo duplicate-command-test",
		Name:    "Second Cmd",
	}
	var result model.AddResult
	err := env.client.Call(model.MethodAdd, params, &result)
	if err == nil {
		t.Fatal("expected error for duplicate command, got nil")
	}

	if !strings.Contains(err.Error(), "duplicate") && !strings.Contains(err.Error(), "exact_command") {
		t.Errorf("expected duplicate/exact_command error, got: %v", err)
	}

	// Force should bypass duplicate check.
	params.Force = true
	var forceResult model.AddResult
	if err := env.client.Call(model.MethodAdd, params, &forceResult); err != nil {
		t.Fatalf("force add should succeed: %v", err)
	}
	if forceResult.ID == "" {
		t.Error("expected non-empty ID from force add")
	}
}

func TestAddDryRun(t *testing.T) {
	// Dry-run is CLI-side; we test that trigger parsing works and
	// NextFireTime returns a future time.
	cfg := model.TriggerConfig{
		Type: model.TriggerTypeCron,
		Cron: "0 9 * * 1-5",
	}

	trig, err := trigger.ParseTrigger(cfg)
	if err != nil {
		t.Fatalf("parse trigger: %v", err)
	}

	now := time.Now()
	next := trig.NextFireTime(now)

	if next.IsZero() {
		t.Fatal("expected non-zero next fire time")
	}
	if !next.After(now) {
		t.Errorf("expected next fire time %v to be after now %v", next, now)
	}

	desc := trig.HumanDescription()
	if desc == "" {
		t.Error("expected non-empty human description")
	}
}

func TestRemoveNonexistent(t *testing.T) {
	env := setupTestDaemon(t)

	// Try to remove a schedule that doesn't exist.
	err := env.client.Call(model.MethodRemove, model.RemoveParams{ID: "sch_000000"}, nil)
	if err == nil {
		t.Fatal("expected error when removing nonexistent schedule")
	}

	// The daemon returns RPCErrCodeNotFound; the client surfaces it as an error.
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' error, got: %v", err)
	}
}

func TestReload(t *testing.T) {
	env := setupTestDaemon(t)

	// Add a schedule via RPC so the daemon has one.
	addTestSchedule(t, env, "Pre-Reload", "echo pre-reload")

	// Manually write a new desired state file to the data repo.
	ds := store.NewDesiredStore(env.cfg.DataRepo)
	newID := "sch_reload"
	newDesired := &model.DesiredState{
		ID:        newID,
		Name:      "Injected Schedule",
		CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{
			Type: model.TriggerTypeCron,
			Cron: "30 14 * * *",
		},
		Command: "echo injected",
		Status:  model.StatusActive,
	}
	if err := ds.Write(newDesired); err != nil {
		t.Fatalf("write desired state: %v", err)
	}

	// Git-commit the new file so the repo stays clean.
	commitFile(t, env.dataDir, ds.FilePath(newID), "test: inject schedule for reload")

	// Call reload.
	var reloadResult model.ReloadResult
	if err := env.client.Call(model.MethodReload, nil, &reloadResult); err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloadResult.Reconciled < 2 {
		t.Errorf("expected at least 2 reconciled schedules, got %d", reloadResult.Reconciled)
	}

	// Verify the injected schedule appears in list.
	var listResult model.ListResult
	if err := env.client.Call(model.MethodList, nil, &listResult); err != nil {
		t.Fatalf("list after reload: %v", err)
	}

	found := false
	for _, s := range listResult.Schedules {
		if s.ID == newID {
			found = true
			if s.Name != "Injected Schedule" {
				t.Errorf("expected name %q, got %q", "Injected Schedule", s.Name)
			}
			break
		}
	}
	if !found {
		t.Errorf("injected schedule %s not found in list after reload", newID)
	}
}

func TestHealth(t *testing.T) {
	env := setupTestDaemon(t)

	var healthResult model.HealthResult
	if err := env.client.Call(model.MethodHealth, nil, &healthResult); err != nil {
		t.Fatalf("health: %v", err)
	}

	if !healthResult.DaemonRunning {
		t.Error("expected daemon_running=true")
	}
	if !healthResult.ConfigValid {
		t.Error("expected config_valid=true")
	}
	if !healthResult.DataRepoOK {
		t.Error("expected data_repo_ok=true")
	}
	if !healthResult.Healthy {
		t.Error("expected healthy=true")
	}
}

func TestReconcileOnStartup(t *testing.T) {
	env := setupTestDaemonNoStart(t)

	// Pre-populate the data repo with a desired state file.
	ds := store.NewDesiredStore(env.cfg.DataRepo)
	if err := ds.EnsureDir(); err != nil {
		t.Fatalf("ensure desired store dir: %v", err)
	}

	preID := "sch_pre001"
	preDesired := &model.DesiredState{
		ID:        preID,
		Name:      "Pre-existing Schedule",
		CreatedAt: time.Now(),
		Trigger: model.TriggerConfig{
			Type: model.TriggerTypeCron,
			Cron: "0 8 * * *",
		},
		Command: "echo pre-existing",
		Status:  model.StatusActive,
	}
	if err := ds.Write(preDesired); err != nil {
		t.Fatalf("write pre-existing desired state: %v", err)
	}

	// Git-commit so the repo is clean for the daemon.
	commitFile(t, env.dataDir, ds.FilePath(preID), "test: pre-populate schedule")

	// Now start the daemon.
	if err := env.daemon.Start(); err != nil {
		t.Fatalf("start daemon: %v", err)
	}
	waitForSocket(t, env.client)

	// Verify the pre-existing schedule was reconciled.
	var listResult model.ListResult
	if err := env.client.Call(model.MethodList, nil, &listResult); err != nil {
		t.Fatalf("list: %v", err)
	}

	if len(listResult.Schedules) != 1 {
		t.Fatalf("expected 1 schedule from reconciliation, got %d", len(listResult.Schedules))
	}
	if listResult.Schedules[0].ID != preID {
		t.Errorf("expected schedule ID %q, got %q", preID, listResult.Schedules[0].ID)
	}
	if listResult.Schedules[0].Name != "Pre-existing Schedule" {
		t.Errorf("expected name %q, got %q", "Pre-existing Schedule", listResult.Schedules[0].Name)
	}
}

func TestConcurrentAdds(t *testing.T) {
	env := setupTestDaemon(t)

	const n = 5

	// Add schedules sequentially (git operations require index.lock exclusivity),
	// then verify concurrently that the daemon serves reads under load.
	ids := make([]string, n)
	for i := 0; i < n; i++ {
		params := model.AddParams{
			Type:    model.TriggerTypeCron,
			Cron:    "0 9 * * 1-5",
			Command: fmt.Sprintf("echo concurrent-%d", i),
			Name:    fmt.Sprintf("Concurrent Job %d", i),
		}
		var result model.AddResult
		if err := env.client.Call(model.MethodAdd, params, &result); err != nil {
			t.Fatalf("add %d: %v", i, err)
		}
		ids[i] = result.ID
	}

	// Verify all 5 schedules appear in list.
	var listResult model.ListResult
	if err := env.client.Call(model.MethodList, nil, &listResult); err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(listResult.Schedules) != n {
		t.Errorf("expected %d schedules, got %d", n, len(listResult.Schedules))
	}

	// Launch concurrent reads (list, show, health) to verify the daemon
	// handles concurrent RPC sessions safely.
	var wg sync.WaitGroup
	readErrs := make([]error, n*3)

	for i := 0; i < n; i++ {
		idx := i

		// Concurrent list.
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lr model.ListResult
			readErrs[idx*3] = env.client.Call(model.MethodList, nil, &lr)
		}()

		// Concurrent show.
		wg.Add(1)
		go func() {
			defer wg.Done()
			var sr model.ShowResult
			readErrs[idx*3+1] = env.client.Call(model.MethodShow, model.ShowParams{ID: ids[idx]}, &sr)
		}()

		// Concurrent health.
		wg.Add(1)
		go func() {
			defer wg.Done()
			var hr model.HealthResult
			readErrs[idx*3+2] = env.client.Call(model.MethodHealth, nil, &hr)
		}()
	}
	wg.Wait()

	for i, err := range readErrs {
		if err != nil {
			t.Errorf("concurrent read %d failed: %v", i, err)
		}
	}
}

// ---------- Helpers ----------

// commitFile stages and commits a single file in the test git repo.
func commitFile(t *testing.T, repoDir, filePath, message string) {
	t.Helper()
	cmds := [][]string{
		{"git", "add", "--", filePath},
		{"git", "commit", "--only", "-m", message, "--", filePath},
	}
	for _, args := range cmds {
		cmd := exec.Command(args[0], args[1:]...)
		cmd.Dir = repoDir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git command %v failed: %v\n%s", args, err, out)
		}
	}
}
