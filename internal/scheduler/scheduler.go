// Package scheduler provides cron-based background execution of recipes.
// Schedules persist in SQLite and survive restarts. On trigger, each
// schedule creates a new session, loads its recipe, and dispatches a task
// through the orchestrator. Extracted from Goose's scheduler.rs pattern.
package scheduler

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/robfig/cron/v3"

	"github.com/rohanrgit/ag3nts/internal/logging"
	"github.com/rohanrgit/ag3nts/internal/recipe"
	"github.com/rohanrgit/ag3nts/internal/store"
	"github.com/rohanrgit/ag3nts/internal/task"
)

// TaskDispatcher is the interface used to dispatch tasks from the scheduler.
// Satisfied by *orchestrator.Orchestrator.
type TaskDispatcher interface {
	CreateTask(t *task.Task) error
}

// Schedule represents a cron-triggered recipe execution.
type Schedule struct {
	ID        string
	Name      string
	Cron      string            // 5-field cron expression
	Recipe    string            // recipe name
	Params    map[string]string // recipe parameters
	Enabled   bool
	LastRun   time.Time
	NextRun   time.Time
	CreatedAt time.Time
}

// Scheduler manages cron schedules backed by SQLite.
type Scheduler struct {
	cron       *cron.Cron
	db         *store.DB
	loader     *recipe.Loader
	dispatcher TaskDispatcher
	logger     *logging.Logger
	entryMap   map[string]cron.EntryID // schedule ID → cron entry ID
	mu         sync.Mutex
}

// New creates a scheduler. Call Start() to begin processing.
func New(db *store.DB, loader *recipe.Loader, dispatcher TaskDispatcher, logger *logging.Logger) *Scheduler {
	return &Scheduler{
		cron:       cron.New(),
		db:         db,
		loader:     loader,
		dispatcher: dispatcher,
		logger:     logger,
		entryMap:   make(map[string]cron.EntryID),
	}
}

// Start loads persisted schedules from SQLite and begins the cron runner.
func (s *Scheduler) Start(ctx context.Context) error {
	schedules, err := s.db.ListSchedules()
	if err != nil {
		return fmt.Errorf("load schedules: %w", err)
	}

	for _, rec := range schedules {
		if !rec.Enabled {
			continue
		}
		sched := fromRecord(rec)
		if err := s.register(sched); err != nil {
			if s.logger != nil {
				s.logger.Errorf("scheduler", "failed to register %q: %v", sched.Name, err)
			}
		}
	}

	s.cron.Start()

	// Stop cron when context is cancelled.
	go func() {
		<-ctx.Done()
		s.cron.Stop()
	}()

	if s.logger != nil {
		s.logger.Infof("scheduler", "started with %d active schedules", len(s.entryMap))
	}
	return nil
}

// Stop shuts down the cron runner.
func (s *Scheduler) Stop() {
	s.cron.Stop()
}

// Add creates a new schedule, persists it, and registers with the cron runner.
func (s *Scheduler) Add(sched *Schedule) error {
	// Validate cron expression.
	if _, err := cron.ParseStandard(sched.Cron); err != nil {
		return fmt.Errorf("invalid cron expression %q: %w", sched.Cron, err)
	}

	// Validate recipe exists.
	if _, err := s.loader.Get(sched.Recipe); err != nil {
		return fmt.Errorf("recipe %q: %w", sched.Recipe, err)
	}

	if sched.ID == "" {
		sched.ID = fmt.Sprintf("sched_%d", time.Now().UnixNano()%100000)
	}
	if sched.CreatedAt.IsZero() {
		sched.CreatedAt = time.Now().UTC()
	}
	sched.Enabled = true

	if err := s.db.CreateSchedule(sched.toRecord()); err != nil {
		return fmt.Errorf("persist schedule: %w", err)
	}

	if err := s.register(sched); err != nil {
		return fmt.Errorf("register cron: %w", err)
	}

	if s.logger != nil {
		s.logger.Infof("scheduler", "added schedule %q (%s) for recipe %q", sched.Name, sched.Cron, sched.Recipe)
	}
	return nil
}

// Remove deletes a schedule from SQLite and unregisters from cron.
func (s *Scheduler) Remove(id string) error {
	s.mu.Lock()
	if entryID, ok := s.entryMap[id]; ok {
		s.cron.Remove(entryID)
		delete(s.entryMap, id)
	}
	s.mu.Unlock()

	return s.db.DeleteSchedule(id)
}

// List returns all schedules from SQLite.
func (s *Scheduler) List() ([]*Schedule, error) {
	records, err := s.db.ListSchedules()
	if err != nil {
		return nil, err
	}
	var schedules []*Schedule
	for _, rec := range records {
		schedules = append(schedules, fromRecord(rec))
	}
	return schedules, nil
}

// RunNow triggers a schedule immediately, outside its cron timing.
func (s *Scheduler) RunNow(id string) error {
	rec, err := s.db.GetSchedule(id)
	if err != nil {
		return err
	}
	if rec == nil {
		return fmt.Errorf("schedule %q not found", id)
	}
	sched := fromRecord(rec)
	go s.execute(sched)
	return nil
}

// register adds a schedule to the cron runner.
func (s *Scheduler) register(sched *Schedule) error {
	entryID, err := s.cron.AddFunc(sched.Cron, func() {
		s.execute(sched)
	})
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.entryMap[sched.ID] = entryID
	s.mu.Unlock()

	// Update next run time.
	entry := s.cron.Entry(entryID)
	if !entry.Next.IsZero() {
		_ = s.db.UpdateScheduleNextRun(sched.ID, entry.Next)
	}

	return nil
}

// execute runs a schedule's recipe as a dispatched task.
func (s *Scheduler) execute(sched *Schedule) {
	if s.logger != nil {
		s.logger.Infof("scheduler", "executing schedule %q (recipe: %s)", sched.Name, sched.Recipe)
	}

	// Load recipe.
	r, err := s.loader.Get(sched.Recipe)
	if err != nil {
		if s.logger != nil {
			s.logger.Errorf("scheduler", "recipe load failed for %q: %v", sched.Recipe, err)
		}
		return
	}

	// Render prompt with params.
	prompt, err := r.RenderPrompt(sched.Params)
	if err != nil {
		if s.logger != nil {
			s.logger.Errorf("scheduler", "prompt render failed for %q: %v", sched.Name, err)
		}
		return
	}

	// Dispatch as task.
	t := &task.Task{
		ID:          fmt.Sprintf("S%s_%d", sched.ID, time.Now().UnixNano()%10000),
		Description: prompt,
		Type:        r.TaskType(),
		Agent:       r.Agent,
		Status:      task.StatusPending,
	}
	if err := s.dispatcher.CreateTask(t); err != nil {
		if s.logger != nil {
			s.logger.Errorf("scheduler", "dispatch failed for %q: %v", sched.Name, err)
		}
		return
	}

	// Update last/next run times.
	now := time.Now().UTC()
	_ = s.db.UpdateScheduleLastRun(sched.ID, now)

	s.mu.Lock()
	if entryID, ok := s.entryMap[sched.ID]; ok {
		entry := s.cron.Entry(entryID)
		if !entry.Next.IsZero() {
			_ = s.db.UpdateScheduleNextRun(sched.ID, entry.Next)
		}
	}
	s.mu.Unlock()

	if s.logger != nil {
		s.logger.Infof("scheduler", "dispatched task %s for schedule %q", t.ID, sched.Name)
	}
}

// Conversion helpers.

func (sched *Schedule) toRecord() *store.ScheduleRecord {
	return &store.ScheduleRecord{
		ID:        sched.ID,
		Name:      sched.Name,
		Cron:      sched.Cron,
		Recipe:    sched.Recipe,
		Params:    sched.Params,
		Enabled:   sched.Enabled,
		LastRun:   sched.LastRun,
		NextRun:   sched.NextRun,
		CreatedAt: sched.CreatedAt,
	}
}

func fromRecord(rec *store.ScheduleRecord) *Schedule {
	return &Schedule{
		ID:        rec.ID,
		Name:      rec.Name,
		Cron:      rec.Cron,
		Recipe:    rec.Recipe,
		Params:    rec.Params,
		Enabled:   rec.Enabled,
		LastRun:   rec.LastRun,
		NextRun:   rec.NextRun,
		CreatedAt: rec.CreatedAt,
	}
}
