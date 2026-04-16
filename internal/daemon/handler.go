package daemon

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fyang0507/sundial/internal/config"
	"github.com/fyang0507/sundial/internal/model"
	"github.com/fyang0507/sundial/internal/similarity"
	"github.com/fyang0507/sundial/internal/trigger"
)

// Handle dispatches an RPC request to the appropriate handler method.
// It implements the ipc.Handler interface.
func (d *Daemon) Handle(method string, params json.RawMessage) (interface{}, *model.RPCError) {
	switch method {
	case model.MethodAdd:
		var p model.AddParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &model.RPCError{
				Code:    model.RPCErrCodeInvalidParams,
				Message: "invalid add params: " + err.Error(),
			}
		}
		return d.handleAdd(p)

	case model.MethodRemove:
		var p model.RemoveParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &model.RPCError{
				Code:    model.RPCErrCodeInvalidParams,
				Message: "invalid remove params: " + err.Error(),
			}
		}
		return d.handleRemove(p)

	case model.MethodPause:
		var p model.PauseParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &model.RPCError{
				Code:    model.RPCErrCodeInvalidParams,
				Message: "invalid pause params: " + err.Error(),
			}
		}
		return d.handlePause(p)

	case model.MethodUnpause:
		var p model.PauseParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &model.RPCError{
				Code:    model.RPCErrCodeInvalidParams,
				Message: "invalid unpause params: " + err.Error(),
			}
		}
		return d.handleUnpause(p)

	case model.MethodList:
		return d.handleList()

	case model.MethodShow:
		var p model.ShowParams
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &model.RPCError{
				Code:    model.RPCErrCodeInvalidParams,
				Message: "invalid show params: " + err.Error(),
			}
		}
		return d.handleShow(p)

	case model.MethodReload:
		return d.handleReload()

	case model.MethodHealth:
		return d.handleHealth()

	default:
		info := model.MethodNotFoundInfo{
			Method:           method,
			AvailableMethods: availableMethods,
		}
		data, _ := json.Marshal(info)
		return nil, &model.RPCError{
			Code:    model.RPCErrCodeMethodNotFound,
			Message: fmt.Sprintf("unknown method %q", method),
			Data:    data,
		}
	}
}

// handleAdd creates a new schedule.
func (d *Daemon) handleAdd(p model.AddParams) (*model.AddResult, *model.RPCError) {
	// 1. Build TriggerConfig from params.
	trigCfg := model.TriggerConfig{
		Type:           p.Type,
		Cron:           p.Cron,
		Event:          p.Event,
		Offset:         p.Offset,
		Days:           p.Days,
		TriggerCommand: p.TriggerCommand,
		Interval:       p.Interval,
		Timeout:        p.Timeout,
	}
	if p.Lat != nil && p.Lon != nil {
		tz := p.Timezone
		if tz == "" {
			tz = "UTC"
		}
		trigCfg.Location = &model.Location{
			Lat:      *p.Lat,
			Lon:      *p.Lon,
			Timezone: tz,
		}
	}

	// 2. Parse and validate trigger.
	trig, err := trigger.ParseTrigger(trigCfg)
	if err != nil {
		return nil, invalidTriggerError(p.Type, err)
	}

	// 3. Check duplicates against active schedules (exact then fuzzy).
	d.mu.RLock()
	if !p.Force {
		var fuzzyDup *model.DuplicateInfo
		for _, sched := range d.schedules {
			// Exact checks first — these take priority.
			if p.Name != "" && sched.desired.Name == p.Name {
				d.mu.RUnlock()
				dupInfo := &model.DuplicateInfo{
					ExistingID:   sched.desired.ID,
					ExistingName: sched.desired.Name,
					MatchType:    "exact_name",
				}
				data, _ := json.Marshal(dupInfo)
				return nil, &model.RPCError{
					Code:    model.RPCErrCodeDuplicate,
					Message: fmt.Sprintf("duplicate schedule: %s match with %s", dupInfo.MatchType, dupInfo.ExistingID),
					Data:    data,
				}
			}
			if sched.desired.Command == p.Command {
				d.mu.RUnlock()
				dupInfo := &model.DuplicateInfo{
					ExistingID:   sched.desired.ID,
					ExistingName: sched.desired.Name,
					MatchType:    "exact_command",
				}
				data, _ := json.Marshal(dupInfo)
				return nil, &model.RPCError{
					Code:    model.RPCErrCodeDuplicate,
					Message: fmt.Sprintf("duplicate schedule: %s match with %s", dupInfo.MatchType, dupInfo.ExistingID),
					Data:    data,
				}
			}

			// Fuzzy checks — keep first match found.
			if fuzzyDup == nil {
				if p.Name != "" && similarity.IsFuzzyNameMatch(p.Name, sched.desired.Name) {
					fuzzyDup = &model.DuplicateInfo{
						ExistingID:   sched.desired.ID,
						ExistingName: sched.desired.Name,
						MatchType:    "fuzzy_name",
					}
				} else if similarity.IsFuzzyCommandMatch(p.Command, sched.desired.Command) {
					fuzzyDup = &model.DuplicateInfo{
						ExistingID:   sched.desired.ID,
						ExistingName: sched.desired.Name,
						MatchType:    "fuzzy_command",
					}
				}
			}
		}

		// Report fuzzy match only if no exact match was found.
		if fuzzyDup != nil {
			d.mu.RUnlock()
			data, _ := json.Marshal(fuzzyDup)
			return nil, &model.RPCError{
				Code:    model.RPCErrCodeDuplicate,
				Message: fmt.Sprintf("duplicate schedule: %s match with %s", fuzzyDup.MatchType, fuzzyDup.ExistingID),
				Data:    data,
			}
		}
	}
	d.mu.RUnlock()

	// 3b. Check for completed schedules with matching command — reactivate instead of creating new.
	if completed := d.findCompletedByCommand(p.Command); completed != nil {
		return d.reactivateSchedule(completed, trigCfg, trig, p)
	}

	// 4. Check git preconditions.
	if err := d.gitOps.CheckRepoPreconditions(); err != nil {
		return nil, d.gitPreconditionError(err)
	}

	// 5. Generate ID, build DesiredState, write to store.
	id := model.NewScheduleID()
	name := p.Name
	if name == "" {
		name = id
	}

	desired := &model.DesiredState{
		ID:          id,
		Name:        name,
		CreatedAt:   time.Now(),
		UserRequest: p.UserRequest,
		Trigger:     trigCfg,
		Command:     p.Command,
		Status:      model.StatusActive,
		Once:        p.Once,
	}

	filePath := d.desiredStore.FilePath(id)
	relPath, _ := filepath.Rel(d.cfg.DataRepo, filePath)

	// Check file preconditions (file doesn't exist yet, so check won't fail on the
	// new file, but we ensure no stale version exists).
	if err := d.desiredStore.Write(desired); err != nil {
		return nil, &model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "failed to write desired state: " + err.Error(),
		}
	}

	// 6. Git commit.
	commitMsg := fmt.Sprintf("sundial: add schedule %s (%s)", id, name)
	if err := d.gitOps.CommitSchedule(filePath, commitMsg); err != nil {
		return nil, d.gitPreconditionError(fmt.Errorf("git commit failed: %w", err))
	}

	// 7. Create RuntimeState, write to store.
	nextFire := trig.NextFireTime(time.Now())
	runtime := &model.RuntimeState{
		ID:         id,
		NextFireAt: nextFire,
	}

	var recovery, warning string
	if err := d.runtimeStore.Write(runtime); err != nil {
		// 8. If runtime write fails, trigger reconcile.
		log.Printf("WARN: runtime write failed for %s, triggering reconcile: %v", id, err)
		recovery = "runtime state write failed; reconciliation triggered"
		warning = err.Error()
		go func() {
			if err := d.reconcile(false); err != nil {
				log.Printf("WARN: reconcile after runtime write failure: %v", err)
			}
		}()
	}

	// 9. Git push (best-effort).
	if err := d.gitOps.Push(); err != nil {
		log.Printf("WARN: git push failed (will retry on reload): %v", err)
		if warning == "" {
			warning = "git push failed: " + err.Error()
		}
	}

	// 10. Add to active schedules, signal wake.
	d.mu.Lock()
	d.schedules[id] = &activeSchedule{
		desired: desired,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()
	d.signalWake()

	// 11. Build result.
	loc := time.Local
	if trigCfg.Location != nil && trigCfg.Location.Timezone != "" {
		if l, err := time.LoadLocation(trigCfg.Location.Timezone); err == nil {
			loc = l
		}
	}

	result := &model.AddResult{
		ID:          id,
		Name:        name,
		Schedule:    trig.HumanDescription(),
		NextFire:    nextFire.In(loc).Format("Mon Jan 2 3:04 PM MST"),
		NextFireUTC: nextFire.UTC().Format(time.RFC3339),
		Status:      "active",
		SavedTo:     relPath,
		Committed:   commitMsg,
		Recovery:    recovery,
		Warning:     warning,
	}

	return result, nil
}

// handleRemove marks schedules as removed.
func (d *Daemon) handleRemove(p model.RemoveParams) (*model.RemoveResult, *model.RPCError) {
	d.mu.Lock()

	var toRemove []string
	if p.All {
		for id := range d.schedules {
			toRemove = append(toRemove, id)
		}
	} else {
		if _, ok := d.schedules[p.ID]; !ok {
			d.mu.Unlock()
			return nil, d.notFoundError(p.ID)
		}
		toRemove = []string{p.ID}
	}
	d.mu.Unlock()

	removed := 0
	var lastCommitMsg string

	for _, id := range toRemove {
		d.mu.RLock()
		sched, ok := d.schedules[id]
		d.mu.RUnlock()
		if !ok {
			continue
		}

		// Update desired state to "removed".
		sched.desired.Status = model.StatusRemoved
		if err := d.desiredStore.Write(sched.desired); err != nil {
			log.Printf("WARN: schedule %s: failed to write removed state: %v", id, err)
			continue
		}

		// Git commit.
		filePath := d.desiredStore.FilePath(id)
		commitMsg := fmt.Sprintf("sundial: remove schedule %s (%s)", id, sched.desired.Name)
		if err := d.gitOps.CommitSchedule(filePath, commitMsg); err != nil {
			return nil, d.gitPreconditionError(fmt.Errorf("git commit failed: %w", err))
		}
		lastCommitMsg = commitMsg

		// Delete runtime state.
		if err := d.runtimeStore.Delete(id); err != nil {
			log.Printf("WARN: schedule %s: failed to delete runtime state: %v", id, err)
		}

		// Remove from active schedules.
		d.mu.Lock()
		delete(d.schedules, id)
		d.mu.Unlock()

		removed++
	}

	d.signalWake()

	// Git push (best-effort).
	var warning string
	if removed > 0 {
		if err := d.gitOps.Push(); err != nil {
			log.Printf("WARN: git push failed after remove: %v", err)
			warning = "git push failed: " + err.Error()
		}
	}

	result := &model.RemoveResult{
		Removed:   removed,
		Committed: lastCommitMsg,
		Warning:   warning,
	}
	if !p.All && len(toRemove) == 1 {
		result.ID = toRemove[0]
	}

	return result, nil
}

// handlePause pauses an active schedule so it stops firing.
func (d *Daemon) handlePause(p model.PauseParams) (*model.PauseResult, *model.RPCError) {
	d.mu.RLock()
	sched, ok := d.schedules[p.ID]
	d.mu.RUnlock()

	if !ok {
		return nil, d.notFoundError(p.ID)
	}

	if sched.desired.Status == model.StatusPaused {
		return nil, d.stateConflictError(p.ID, sched.desired.Name, "paused",
			fmt.Sprintf("sundial unpause %s", p.ID))
	}

	// Check git preconditions.
	if err := d.gitOps.CheckRepoPreconditions(); err != nil {
		return nil, d.gitPreconditionError(err)
	}

	// Update desired state to paused.
	sched.desired.Status = model.StatusPaused
	if err := d.desiredStore.Write(sched.desired); err != nil {
		return nil, &model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "failed to write paused state: " + err.Error(),
		}
	}

	// Git commit.
	filePath := d.desiredStore.FilePath(p.ID)
	commitMsg := fmt.Sprintf("sundial: pause schedule %s (%s)", p.ID, sched.desired.Name)
	if err := d.gitOps.CommitSchedule(filePath, commitMsg); err != nil {
		return nil, d.gitPreconditionError(fmt.Errorf("git commit failed: %w", err))
	}

	// Zero out NextFireAt so the schedule won't fire.
	d.mu.Lock()
	sched.runtime.NextFireAt = time.Time{}
	d.mu.Unlock()

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after pause: %v", p.ID, err)
	}

	// Git push (best-effort).
	var warning string
	if err := d.gitOps.Push(); err != nil {
		log.Printf("WARN: git push failed after pause: %v", err)
		warning = "git push failed: " + err.Error()
	}

	d.signalWake()

	return &model.PauseResult{
		ID:        p.ID,
		Name:      sched.desired.Name,
		Status:    "paused",
		Committed: commitMsg,
		Warning:   warning,
	}, nil
}

// handleUnpause resumes a paused schedule.
func (d *Daemon) handleUnpause(p model.PauseParams) (*model.PauseResult, *model.RPCError) {
	d.mu.RLock()
	sched, ok := d.schedules[p.ID]
	d.mu.RUnlock()

	if !ok {
		return nil, d.notFoundError(p.ID)
	}

	if sched.desired.Status != model.StatusPaused {
		return nil, d.stateConflictError(p.ID, sched.desired.Name, string(sched.desired.Status),
			fmt.Sprintf("sundial pause %s", p.ID))
	}

	// Check git preconditions.
	if err := d.gitOps.CheckRepoPreconditions(); err != nil {
		return nil, d.gitPreconditionError(err)
	}

	// Update desired state back to active.
	sched.desired.Status = model.StatusActive
	if err := d.desiredStore.Write(sched.desired); err != nil {
		return nil, &model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "failed to write unpaused state: " + err.Error(),
		}
	}

	// Git commit.
	filePath := d.desiredStore.FilePath(p.ID)
	commitMsg := fmt.Sprintf("sundial: unpause schedule %s (%s)", p.ID, sched.desired.Name)
	if err := d.gitOps.CommitSchedule(filePath, commitMsg); err != nil {
		return nil, d.gitPreconditionError(fmt.Errorf("git commit failed: %w", err))
	}

	// Recompute NextFireAt.
	nextFire := sched.trigger.NextFireTime(time.Now())
	d.mu.Lock()
	sched.runtime.NextFireAt = nextFire
	d.mu.Unlock()

	if err := d.runtimeStore.Write(sched.runtime); err != nil {
		log.Printf("WARN: schedule %s: failed to persist runtime after unpause: %v", p.ID, err)
	}

	// Git push (best-effort).
	var warning string
	if err := d.gitOps.Push(); err != nil {
		log.Printf("WARN: git push failed after unpause: %v", err)
		warning = "git push failed: " + err.Error()
	}

	d.signalWake()

	loc := time.Local
	if sched.desired.Trigger.Location != nil && sched.desired.Trigger.Location.Timezone != "" {
		if l, err := time.LoadLocation(sched.desired.Trigger.Location.Timezone); err == nil {
			loc = l
		}
	}

	return &model.PauseResult{
		ID:        p.ID,
		Name:      sched.desired.Name,
		Status:    "active",
		NextFire:  nextFire.In(loc).Format("Mon Jan 2 3:04 PM MST"),
		Committed: commitMsg,
		Warning:   warning,
	}, nil
}

// handleList returns a summary of all active schedules.
func (d *Daemon) handleList() (*model.ListResult, *model.RPCError) {
	d.mu.RLock()
	defer d.mu.RUnlock()

	schedules := make([]model.ScheduleSummary, 0, len(d.schedules))
	for _, sched := range d.schedules {
		summary := buildSummary(sched)
		schedules = append(schedules, summary)
	}

	return &model.ListResult{Schedules: schedules}, nil
}

// handleShow returns detailed info for a single schedule.
func (d *Daemon) handleShow(p model.ShowParams) (*model.ShowResult, *model.RPCError) {
	d.mu.RLock()
	sched, ok := d.schedules[p.ID]
	d.mu.RUnlock()

	if !ok {
		return nil, d.notFoundError(p.ID)
	}

	summary := buildSummary(sched)

	// Count missed fires from run logs.
	missedCount, err := d.runLogStore.MissedSince(sched.desired.ID, sched.desired.CreatedAt)
	if err != nil {
		log.Printf("WARN: schedule %s: failed to count misses: %v", p.ID, err)
	}
	summary.MissedCount = missedCount

	result := &model.ShowResult{
		ScheduleSummary:   summary,
		Command:           sched.desired.Command,
		UserRequest:       sched.desired.UserRequest,
		CreatedAt:         sched.desired.CreatedAt.Format(time.RFC3339),
		RecreationCommand: sched.desired.RecreationCommand,
	}

	return result, nil
}

// handleReload triggers a reconciliation and retries pending pushes.
func (d *Daemon) handleReload() (*model.ReloadResult, *model.RPCError) {
	if err := d.reconcile(false); err != nil {
		return nil, &model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "reconciliation failed: " + err.Error(),
		}
	}

	// Retry pending pushes.
	pendingPushes := false
	hasPending, err := d.gitOps.HasPendingPushes()
	if err != nil {
		log.Printf("WARN: failed to check pending pushes: %v", err)
	} else if hasPending {
		pendingPushes = true
		if err := d.gitOps.Push(); err != nil {
			log.Printf("WARN: retry push failed: %v", err)
		} else {
			pendingPushes = false
		}
	}

	d.mu.RLock()
	count := len(d.schedules)
	d.mu.RUnlock()

	return &model.ReloadResult{
		Reconciled:    count,
		PendingPushes: pendingPushes,
		Message:       fmt.Sprintf("reconciled %d schedules", count),
	}, nil
}

// handleHealth runs all health checks and returns the result.
func (d *Daemon) handleHealth() (*model.HealthResult, *model.RPCError) {
	result := &model.HealthResult{
		DaemonRunning: true,
		Checks:        make([]model.HealthCheck, 0),
	}

	// Daemon running check.
	result.Checks = append(result.Checks, model.HealthCheck{
		Name:   "daemon_running",
		Status: "ok",
	})

	// Config valid check.
	configErr := config.Validate(d.cfg)
	result.ConfigValid = configErr == nil
	if configErr == nil {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:   "config_valid",
			Status: "ok",
		})
	} else {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "config_valid",
			Status:  "error",
			Message: configErr.Error(),
		})
	}

	// Data repo check.
	repoExists := false
	if info, err := os.Stat(d.cfg.DataRepo); err == nil && info.IsDir() {
		gitDir := filepath.Join(d.cfg.DataRepo, ".git")
		if info, err := os.Stat(gitDir); err == nil && info.IsDir() {
			repoExists = true
		}
	}
	result.DataRepoOK = repoExists
	if repoExists {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:   "data_repo",
			Status: "ok",
		})
	} else {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "data_repo",
			Status:  "error",
			Message: "data repo path does not exist or is not a git repository",
		})
	}

	// Git status check.
	gitClean := true
	preErr := d.gitOps.CheckRepoPreconditions()
	if preErr != nil {
		gitClean = false
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "git_preconditions",
			Status:  "warn",
			Message: preErr.Error(),
		})
	} else {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:   "git_preconditions",
			Status: "ok",
		})
	}

	// Pending pushes.
	hasPending, err := d.gitOps.HasPendingPushes()
	if err != nil {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "pending_pushes",
			Status:  "warn",
			Message: err.Error(),
		})
	} else if hasPending {
		result.PendingPushes = true
		gitClean = false
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "pending_pushes",
			Status:  "warn",
			Message: "local commits not yet pushed",
		})
	} else {
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:   "pending_pushes",
			Status: "ok",
		})
	}

	// Modified schedule files.
	schedulesDir := filepath.Join(d.cfg.DataRepo, "sundial", "schedules")
	modFiles, err := d.gitOps.ListModifiedScheduleFiles(schedulesDir)
	if err != nil {
		log.Printf("WARN: failed to list modified schedule files: %v", err)
	}
	if len(modFiles) > 0 {
		gitClean = false
		result.ScheduleFileWarnings = modFiles
		result.Checks = append(result.Checks, model.HealthCheck{
			Name:    "schedule_files",
			Status:  "warn",
			Message: fmt.Sprintf("%d schedule files have local modifications", len(modFiles)),
		})
	}
	result.DataRepoGitClean = gitClean

	// Schedule count.
	d.mu.RLock()
	result.ScheduleCount = len(d.schedules)

	// Orphaned schedules (runtime without desired).
	// Since we track only active schedules, we detect orphans from runtime store.
	d.mu.RUnlock()

	runtimeList, err := d.runtimeStore.List()
	if err == nil {
		d.mu.RLock()
		for _, rs := range runtimeList {
			if _, ok := d.schedules[rs.ID]; !ok {
				result.OrphanedSchedules = append(result.OrphanedSchedules, rs.ID)
			}
		}
		d.mu.RUnlock()
	}

	// Effective PATH.
	result.EffectivePath = os.Getenv("PATH")

	// Overall healthy status.
	result.Healthy = result.DaemonRunning && result.ConfigValid && result.DataRepoOK && result.DataRepoGitClean

	return result, nil
}

// findCompletedByCommand scans the desired store for a completed schedule
// with a matching command. Returns nil if none found.
func (d *Daemon) findCompletedByCommand(command string) *model.DesiredState {
	desiredList, err := d.desiredStore.List()
	if err != nil {
		log.Printf("WARN: failed to list desired states for reactivation check: %v", err)
		return nil
	}
	for _, ds := range desiredList {
		if ds.Status == model.StatusCompleted && ds.Command == command {
			return ds
		}
	}
	return nil
}

// reactivateSchedule resets a completed schedule back to active with updated
// trigger config and runtime state.
func (d *Daemon) reactivateSchedule(
	completed *model.DesiredState,
	trigCfg model.TriggerConfig,
	trig model.Trigger,
	p model.AddParams,
) (*model.AddResult, *model.RPCError) {
	id := completed.ID
	name := completed.Name

	// Check git preconditions.
	if err := d.gitOps.CheckRepoPreconditions(); err != nil {
		return nil, d.gitPreconditionError(err)
	}

	// Update desired state.
	completed.Status = model.StatusActive
	completed.Trigger = trigCfg
	completed.Once = p.Once
	if p.Name != "" {
		completed.Name = p.Name
		name = p.Name
	}
	if p.UserRequest != "" {
		completed.UserRequest = p.UserRequest
	}

	if err := d.desiredStore.Write(completed); err != nil {
		return nil, &model.RPCError{
			Code:    model.RPCErrCodeInternal,
			Message: "failed to write reactivated desired state: " + err.Error(),
		}
	}

	// Git commit.
	filePath := d.desiredStore.FilePath(id)
	relPath, _ := filepath.Rel(d.cfg.DataRepo, filePath)
	commitMsg := fmt.Sprintf("sundial: reactivate schedule %s (%s)", id, name)
	if err := d.gitOps.CommitSchedule(filePath, commitMsg); err != nil {
		return nil, d.gitPreconditionError(fmt.Errorf("git commit failed: %w", err))
	}

	// Create fresh runtime state.
	nextFire := trig.NextFireTime(time.Now())
	runtime := &model.RuntimeState{
		ID:         id,
		NextFireAt: nextFire,
	}

	var recovery, warning string
	if err := d.runtimeStore.Write(runtime); err != nil {
		log.Printf("WARN: runtime write failed for reactivated %s, triggering reconcile: %v", id, err)
		recovery = "runtime state write failed; reconciliation triggered"
		warning = err.Error()
		go func() {
			if err := d.reconcile(false); err != nil {
				log.Printf("WARN: reconcile after runtime write failure: %v", err)
			}
		}()
	}

	// Git push (best-effort).
	if err := d.gitOps.Push(); err != nil {
		log.Printf("WARN: git push failed (will retry on reload): %v", err)
		if warning == "" {
			warning = "git push failed: " + err.Error()
		}
	}

	// Add to active schedules, signal wake.
	d.mu.Lock()
	d.schedules[id] = &activeSchedule{
		desired: completed,
		runtime: runtime,
		trigger: trig,
	}
	d.mu.Unlock()
	d.signalWake()

	log.Printf("schedule %s (%s): reactivated from completed state", id, name)

	result := &model.AddResult{
		ID:          id,
		Name:        name,
		Schedule:    trig.HumanDescription(),
		NextFire:    nextFire.Local().Format("Mon Jan 2 3:04 PM MST"),
		NextFireUTC: nextFire.UTC().Format(time.RFC3339),
		Status:      "reactivated",
		SavedTo:     relPath,
		Committed:   commitMsg,
		Recovery:    recovery,
		Warning:     warning,
	}

	return result, nil
}

// notFoundError builds a structured RPCError for schedule-not-found, including
// available IDs and closest match to help agents self-correct.
func (d *Daemon) notFoundError(searchedID string) *model.RPCError {
	d.mu.RLock()
	defer d.mu.RUnlock()

	info := model.NotFoundInfo{
		SearchedID: searchedID,
		Hint:       `run "sundial list" to see available schedules`,
	}

	const maxIDs = 10
	var bestID string
	bestDist := 1.0

	for id, sched := range d.schedules {
		if len(info.AvailableIDs) < maxIDs {
			info.AvailableIDs = append(info.AvailableIDs, id+" ("+sched.desired.Name+")")
		}
		dist := similarity.NormalizedDistance(searchedID, id)
		if dist < bestDist {
			bestDist = dist
			bestID = id
		}
	}

	if bestDist <= 0.5 && bestID != "" {
		info.ClosestMatch = bestID
	}

	data, _ := json.Marshal(info)
	return &model.RPCError{
		Code:    model.RPCErrCodeNotFound,
		Message: fmt.Sprintf("schedule %s not found", searchedID),
		Data:    data,
	}
}

// gitPreconditionError builds a structured RPCError for git precondition
// failures, classifying the failure type and providing recovery commands.
func (d *Daemon) gitPreconditionError(err error) *model.RPCError {
	info := model.GitPreconditionInfo{
		DataRepoPath: d.cfg.DataRepo,
	}

	msg := err.Error()
	switch {
	case strings.Contains(msg, "detached HEAD"):
		info.FailureType = "detached_head"
		info.RecoveryCommands = []string{
			fmt.Sprintf("git -C %s checkout main", d.cfg.DataRepo),
		}
	case strings.Contains(msg, "rebase in progress"):
		info.FailureType = "rebase"
		info.RecoveryCommands = []string{
			fmt.Sprintf("git -C %s rebase --abort", d.cfg.DataRepo),
		}
	case strings.Contains(msg, "merge in progress"):
		info.FailureType = "merge"
		info.RecoveryCommands = []string{
			fmt.Sprintf("git -C %s merge --abort", d.cfg.DataRepo),
		}
	case strings.Contains(msg, "unmerged"):
		info.FailureType = "unmerged"
		info.RecoveryCommands = []string{
			fmt.Sprintf("git -C %s add . && git -C %s commit -m 'resolve conflicts'", d.cfg.DataRepo, d.cfg.DataRepo),
		}
	case strings.Contains(msg, "git commit failed") || strings.Contains(msg, "git add failed"):
		info.FailureType = "commit_failed"
		info.RecoveryCommands = []string{"sundial reload"}
	default:
		info.FailureType = "unknown"
		info.RecoveryCommands = []string{"sundial health", "sundial reload"}
	}

	data, _ := json.Marshal(info)
	return &model.RPCError{
		Code:    model.RPCErrCodeGitPrecondition,
		Message: msg,
		Data:    data,
	}
}

// stateConflictError builds a structured RPCError when a pause/unpause operation
// conflicts with the schedule's current state.
func (d *Daemon) stateConflictError(id, name, currentStatus, suggestedCmd string) *model.RPCError {
	info := model.StateConflictInfo{
		ScheduleID:       id,
		ScheduleName:     name,
		CurrentStatus:    currentStatus,
		SuggestedCommand: suggestedCmd,
	}
	data, _ := json.Marshal(info)

	return &model.RPCError{
		Code:    model.RPCErrCodeStateConflict,
		Message: fmt.Sprintf("schedule %s is already %s", id, currentStatus),
		Data:    data,
	}
}

// invalidTriggerError builds a structured RPCError for invalid trigger parameters.
func invalidTriggerError(trigType model.TriggerType, err error) *model.RPCError {
	info := model.InvalidTriggerInfo{
		TriggerType: string(trigType),
		RawError:    err.Error(),
	}
	switch trigType {
	case model.TriggerTypeCron:
		info.Example = `sundial add --type cron --cron "0 9 * * 1-5" --command "echo hello"`
	case model.TriggerTypeSolar:
		info.Example = `sundial add --type solar --event sunset --offset "-1h" --days mon,tue --lat 37.7749 --lon -122.4194 --timezone "America/Los_Angeles" --command "echo hello"`
	case model.TriggerTypePoll:
		info.Example = `sundial add --type poll --trigger 'check-cmd' --interval 2m --timeout 72h --command "echo hello" --once`
	}
	data, _ := json.Marshal(info)

	return &model.RPCError{
		Code:    model.RPCErrCodeInvalidParams,
		Message: "invalid trigger: " + err.Error(),
		Data:    data,
	}
}

// availableMethods returns the list of valid RPC method names.
var availableMethods = []string{
	model.MethodAdd, model.MethodRemove,
	model.MethodPause, model.MethodUnpause,
	model.MethodList, model.MethodShow,
	model.MethodReload, model.MethodHealth,
}

// buildSummary constructs a ScheduleSummary from an activeSchedule.
func buildSummary(sched *activeSchedule) model.ScheduleSummary {
	summary := model.ScheduleSummary{
		ID:       sched.desired.ID,
		Name:     sched.desired.Name,
		Schedule: sched.trigger.HumanDescription(),
		Status:   string(sched.desired.Status),
	}

	if !sched.runtime.NextFireAt.IsZero() {
		summary.NextFire = sched.runtime.NextFireAt.Local().Format("Mon Jan 2 3:04 PM MST")
		summary.NextFireUTC = sched.runtime.NextFireAt.UTC().Format(time.RFC3339)
	}

	if sched.runtime.LastFiredAt != nil {
		summary.LastFire = sched.runtime.LastFiredAt.Local().Format("Mon Jan 2 3:04 PM MST")
	}

	summary.LastExitCode = sched.runtime.LastExitCode

	return summary
}
