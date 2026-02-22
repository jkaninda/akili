package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus"
)

// --- DAG Validation ---

func TestValidateDAG_ValidLinear(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, Description: "research"},
		{AgentRole: RoleExecutor, Description: "execute", DependsOn: []int{0}},
	}
	if err := ValidateDAG(specs); err != nil {
		t.Fatalf("expected valid DAG, got: %v", err)
	}
}

func TestValidateDAG_ValidParallel(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, Description: "research1"},
		{AgentRole: RoleResearcher, Description: "research2"},
		{AgentRole: RoleExecutor, Description: "execute", DependsOn: []int{0, 1}},
	}
	if err := ValidateDAG(specs); err != nil {
		t.Fatalf("expected valid DAG, got: %v", err)
	}
}

func TestValidateDAG_Empty(t *testing.T) {
	if err := ValidateDAG(nil); err != nil {
		t.Fatalf("expected nil DAG to be valid, got: %v", err)
	}
}

func TestValidateDAG_SelfDependency(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, DependsOn: []int{0}},
	}
	err := ValidateDAG(specs)
	if err == nil {
		t.Fatal("expected self-dependency error")
	}
}

func TestValidateDAG_Cycle(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, DependsOn: []int{1}},
		{AgentRole: RoleExecutor, DependsOn: []int{0}},
	}
	err := ValidateDAG(specs)
	if err == nil {
		t.Fatal("expected cycle detection error")
	}
}

func TestValidateDAG_OutOfRange(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, DependsOn: []int{5}},
	}
	err := ValidateDAG(specs)
	if err == nil {
		t.Fatal("expected out-of-range error")
	}
}

func TestResolveDependencies(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher},
		{AgentRole: RoleExecutor, DependsOn: []int{0}},
		{AgentRole: RoleCompliance, DependsOn: []int{0, 1}},
	}
	ids := []uuid.UUID{uuid.New(), uuid.New(), uuid.New()}
	deps := ResolveDependencies(specs, ids)

	if len(deps[0]) != 0 {
		t.Errorf("task 0 should have no deps, got %d", len(deps[0]))
	}
	if len(deps[1]) != 1 || deps[1][0] != ids[0] {
		t.Errorf("task 1 deps = %v, want [%s]", deps[1], ids[0])
	}
	if len(deps[2]) != 2 {
		t.Errorf("task 2 deps = %v, want 2 entries", deps[2])
	}
}

func TestFilterReadyTasks(t *testing.T) {
	id1, id2, id3 := uuid.New(), uuid.New(), uuid.New()

	candidates := []Task{
		{ID: id2, DependsOn: []uuid.UUID{id1}},
		{ID: id3, DependsOn: []uuid.UUID{id1, id2}},
	}
	completed := map[uuid.UUID]bool{id1: true}

	ready := FilterReadyTasks(candidates, completed)
	if len(ready) != 1 || ready[0].ID != id2 {
		t.Errorf("expected only task %s ready, got %v", id2, ready)
	}

	// Now mark id2 as completed.
	completed[id2] = true
	ready = FilterReadyTasks(candidates, completed)
	if len(ready) != 2 {
		t.Errorf("expected 2 ready tasks, got %d", len(ready))
	}
}

// --- InMemoryStore ---

func TestInMemoryStore_WorkflowCRUD(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	wf := &Workflow{
		ID:     uuid.New(),
		OrgID:  uuid.New(),
		UserID: "alice",
		Goal:   "test goal",
		Status: WorkflowPending,
	}

	if err := store.CreateWorkflow(ctx, wf); err != nil {
		t.Fatalf("create: %v", err)
	}

	// Duplicate should fail.
	if err := store.CreateWorkflow(ctx, wf); err == nil {
		t.Fatal("expected duplicate error")
	}

	// Get.
	got, err := store.GetWorkflow(ctx, wf.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Goal != "test goal" {
		t.Errorf("goal = %q, want %q", got.Goal, "test goal")
	}

	// Update.
	wf.Status = WorkflowRunning
	if err := store.UpdateWorkflow(ctx, wf); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ = store.GetWorkflow(ctx, wf.ID)
	if got.Status != WorkflowRunning {
		t.Errorf("status = %q, want %q", got.Status, WorkflowRunning)
	}

	// Not found.
	_, err = store.GetWorkflow(ctx, uuid.New())
	if err == nil {
		t.Fatal("expected not found error")
	}
}

func TestInMemoryStore_TaskCRUD(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	wfID := uuid.New()
	task := &Task{
		ID:          uuid.New(),
		WorkflowID:  wfID,
		AgentRole:   RolePlanner,
		Description: "plan",
		Status:      TaskPending,
		CreatedAt:   time.Now().UTC(),
	}

	if err := store.CreateTask(ctx, task); err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := store.GetTask(ctx, task.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.AgentRole != RolePlanner {
		t.Errorf("role = %q, want %q", got.AgentRole, RolePlanner)
	}

	tasks, err := store.ListTasksByWorkflow(ctx, wfID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("task count = %d, want 1", len(tasks))
	}
}

func TestInMemoryStore_GetReadyTasks(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	wfID := uuid.New()
	t1ID := uuid.New()
	t2ID := uuid.New()

	// t1 has no deps (ready), t2 depends on t1 (not ready).
	t1 := &Task{
		ID: t1ID, WorkflowID: wfID, Status: TaskPending,
		AgentRole: RolePlanner, Description: "plan",
		CreatedAt: time.Now().UTC(),
	}
	t2 := &Task{
		ID: t2ID, WorkflowID: wfID, Status: TaskPending,
		AgentRole: RoleExecutor, Description: "exec",
		DependsOn: []uuid.UUID{t1ID},
		CreatedAt: time.Now().UTC(),
	}

	_ = store.CreateTask(ctx, t1)
	_ = store.CreateTask(ctx, t2)

	ready, err := store.GetReadyTasks(ctx, wfID)
	if err != nil {
		t.Fatalf("get ready: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != t1ID {
		t.Errorf("expected only t1 ready, got %d tasks", len(ready))
	}

	// Complete t1.
	t1.Status = TaskCompleted
	_ = store.UpdateTask(ctx, t1)

	ready, err = store.GetReadyTasks(ctx, wfID)
	if err != nil {
		t.Fatalf("get ready after completion: %v", err)
	}
	if len(ready) != 1 || ready[0].ID != t2ID {
		t.Errorf("expected t2 ready, got %d tasks", len(ready))
	}
}

func TestInMemoryStore_Messages(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	wfID := uuid.New()
	msg := &AgentMessage{
		ID:          uuid.New(),
		WorkflowID:  wfID,
		FromTaskID:  uuid.New(),
		FromRole:    RolePlanner,
		ToRole:      RoleOrchestrator,
		MessageType: MsgPlanProposal,
		Content:     "plan content",
		CreatedAt:   time.Now().UTC(),
	}

	if err := store.SaveMessage(ctx, msg); err != nil {
		t.Fatalf("save: %v", err)
	}

	msgs, err := store.ListMessages(ctx, wfID)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Content != "plan content" {
		t.Errorf("messages = %v, want 1 with 'plan content'", msgs)
	}
}

// --- Metrics ---

func TestWorkflowMetrics_Created(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := NewWorkflowMetrics(reg)
	if m == nil {
		t.Fatal("expected non-nil WorkflowMetrics")
	}

	// Increment to make metrics visible in Gather.
	m.WorkflowsTotal.WithLabelValues("completed").Inc()
	m.TasksTotal.WithLabelValues("planner", "completed").Inc()
	m.AgentInvocationsTotal.WithLabelValues("executor", "success").Inc()

	families, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}

	names := make(map[string]bool)
	for _, f := range families {
		names[f.GetName()] = true
	}
	for _, expected := range []string{
		"akili_workflow_total",
		"akili_workflow_tasks_total",
		"akili_workflow_agent_invocations_total",
	} {
		if !names[expected] {
			t.Errorf("metric %q not found", expected)
		}
	}
}

func TestWorkflowMetrics_NilRegistry(t *testing.T) {
	m := NewWorkflowMetrics(nil)
	if m != nil {
		t.Fatal("expected nil WorkflowMetrics for nil registry")
	}
}

// --- Role Validation ---

func TestValidateRole(t *testing.T) {
	for _, role := range []AgentRole{RoleOrchestrator, RolePlanner, RoleResearcher, RoleExecutor, RoleCompliance} {
		if err := ValidateRole(role); err != nil {
			t.Errorf("ValidateRole(%q) = %v, want nil", role, err)
		}
	}
	if err := ValidateRole("unknown"); err == nil {
		t.Error("expected error for unknown role")
	}
}

// --- Prompts ---

func TestRoleSystemPrompts(t *testing.T) {
	roles := []AgentRole{RoleOrchestrator, RolePlanner, RoleResearcher, RoleExecutor, RoleCompliance}
	for _, role := range roles {
		prompt := roleSystemPrompt(role)
		if prompt == "" {
			t.Errorf("empty system prompt for role %q", role)
		}
	}
	// All should be different.
	seen := make(map[string]bool)
	for _, role := range roles {
		p := roleSystemPrompt(role)
		if seen[p] {
			t.Errorf("duplicate system prompt for role %q", role)
		}
		seen[p] = true
	}
}

// --- Planner Output Parsing ---

func TestParsePlannerOutput_ValidJSON(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, Description: "research", Input: "gather info"},
		{AgentRole: RoleExecutor, Description: "execute", Input: "run command", DependsOn: []int{0}},
	}
	data, _ := json.Marshal(specs)
	output := &AgentOutput{Response: string(data)}

	parsed, err := parsePlannerOutput(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(parsed))
	}
	if parsed[0].AgentRole != RoleResearcher {
		t.Errorf("spec[0].AgentRole = %q, want researcher", parsed[0].AgentRole)
	}
}

func TestParsePlannerOutput_JSONInText(t *testing.T) {
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, Description: "research", Input: "do it"},
	}
	data, _ := json.Marshal(specs)
	output := &AgentOutput{
		Response: fmt.Sprintf("Here is the plan:\n%s\nDone.", string(data)),
	}

	parsed, err := parsePlannerOutput(output)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(parsed) != 1 {
		t.Fatalf("expected 1 spec, got %d", len(parsed))
	}
}

func TestParsePlannerOutput_Empty(t *testing.T) {
	_, err := parsePlannerOutput(&AgentOutput{Response: ""})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestParsePlannerOutput_InvalidJSON(t *testing.T) {
	_, err := parsePlannerOutput(&AgentOutput{Response: "no json here"})
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

// --- Limit Enforcement ---

func TestScheduler_DepthLimit(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	wf := &Workflow{
		ID:       uuid.New(),
		OrgID:    uuid.New(),
		MaxDepth: 2,
		MaxTasks: 50,
	}
	_ = store.CreateWorkflow(ctx, wf)

	plannerTask := &Task{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		AgentRole:  RolePlanner,
		Depth:      1, // Depth 1, maxDepth 2 → children at depth 2 is NOT < 2, should fail.
		CreatedAt:  time.Now().UTC(),
	}
	_ = store.CreateTask(ctx, plannerTask)

	sched := &scheduler{
		store: store,
		config: EngineConfig{
			DefaultMaxDepth: 2,
			DefaultMaxTasks: 50,
		},
	}

	specs := []TaskSpec{
		{AgentRole: RoleResearcher, Description: "research", Input: "do it"},
	}
	data, _ := json.Marshal(specs)
	output := &AgentOutput{Response: string(data)}

	err := sched.handlePlannerOutput(ctx, wf, plannerTask, output)
	if err == nil {
		t.Fatal("expected depth limit error")
	}
}

func TestScheduler_TaskCountLimit(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()

	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		MaxDepth:  10,
		MaxTasks:  3,
		TaskCount: 2,
	}
	_ = store.CreateWorkflow(ctx, wf)

	plannerTask := &Task{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		AgentRole:  RolePlanner,
		Depth:      0,
		CreatedAt:  time.Now().UTC(),
	}
	_ = store.CreateTask(ctx, plannerTask)

	sched := &scheduler{
		store:  store,
		config: EngineConfig{DefaultMaxTasks: 3},
	}

	// Try to add 2 tasks when count is already 2 and limit is 3.
	specs := []TaskSpec{
		{AgentRole: RoleResearcher, Description: "r1", Input: "1"},
		{AgentRole: RoleResearcher, Description: "r2", Input: "2"},
	}
	data, _ := json.Marshal(specs)
	output := &AgentOutput{Response: string(data)}

	err := sched.handlePlannerOutput(ctx, wf, plannerTask, output)
	if err == nil {
		t.Fatal("expected task count limit error")
	}
}

// --- Engine Submit ---

func TestEngine_SubmitDefaults(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	engine := NewEngine(store, factory, nil, nil, nil, EngineConfig{})

	wf, err := engine.Submit(ctx, &WorkflowRequest{
		UserID: "alice",
		Goal:   "deploy app",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	if wf.Status != WorkflowRunning {
		t.Errorf("status = %q, want running", wf.Status)
	}
	if wf.MaxDepth != 5 {
		t.Errorf("max_depth = %d, want 5", wf.MaxDepth)
	}
	if wf.MaxTasks != 50 {
		t.Errorf("max_tasks = %d, want 50", wf.MaxTasks)
	}
	if wf.BudgetLimitUSD != 1.0 {
		t.Errorf("budget = %f, want 1.0", wf.BudgetLimitUSD)
	}
	if wf.TaskCount != 1 {
		t.Errorf("task_count = %d, want 1", wf.TaskCount)
	}
	if wf.RootTaskID == nil {
		t.Error("root_task_id should not be nil")
	}

	// Verify root task exists.
	tasks, _ := store.ListTasksByWorkflow(ctx, wf.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].AgentRole != RolePlanner {
		t.Errorf("root task role = %q, want planner", tasks[0].AgentRole)
	}
}

func TestEngine_StatusAndCancel(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	engine := NewEngine(store, factory, nil, nil, nil, EngineConfig{})

	wf, _ := engine.Submit(ctx, &WorkflowRequest{
		UserID: "alice",
		Goal:   "test",
	})

	// Status should work.
	got, err := engine.Status(ctx, wf.ID)
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	if got.Goal != "test" {
		t.Errorf("goal = %q, want %q", got.Goal, "test")
	}

	// Cancel should not error.
	if err := engine.Cancel(ctx, wf.ID); err != nil {
		t.Fatalf("cancel: %v", err)
	}
}

func TestEngine_ListTasks(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	engine := NewEngine(store, factory, nil, nil, nil, EngineConfig{})

	wf, _ := engine.Submit(ctx, &WorkflowRequest{
		UserID: "alice",
		Goal:   "test",
	})

	tasks, err := engine.ListTasks(ctx, wf.ID)
	if err != nil {
		t.Fatalf("list tasks: %v", err)
	}
	if len(tasks) != 1 {
		t.Errorf("task count = %d, want 1", len(tasks))
	}
}

// --- InMemoryStore Claim Stubs ---

func TestInMemoryStore_ClaimReadyTasks_Unsupported(t *testing.T) {
	store := NewInMemoryStore()
	_, err := store.ClaimReadyTasks(context.Background(), "agent-1", nil, 5)
	if err == nil {
		t.Fatal("expected error for in-memory ClaimReadyTasks")
	}
}

func TestInMemoryStore_HeartbeatTasks_Unsupported(t *testing.T) {
	store := NewInMemoryStore()
	err := store.HeartbeatTasks(context.Background(), "agent-1", []uuid.UUID{uuid.New()})
	if err == nil {
		t.Fatal("expected error for in-memory HeartbeatTasks")
	}
}

// --- ExternalAgents Flag ---

func TestEngine_ExternalAgents_NoScheduler(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	engine := NewEngine(store, factory, nil, nil, nil, EngineConfig{
		ExternalAgents: true,
	})

	wf, err := engine.Submit(ctx, &WorkflowRequest{
		UserID: "alice",
		Goal:   "deploy app",
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}

	// Workflow should be running with a root task.
	if wf.Status != WorkflowRunning {
		t.Errorf("status = %q, want running", wf.Status)
	}
	if wf.RootTaskID == nil {
		t.Error("root_task_id should not be nil")
	}

	// Root task should remain pending (no inline scheduler running).
	tasks, _ := store.ListTasksByWorkflow(ctx, wf.ID)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
	if tasks[0].Status != TaskPending {
		t.Errorf("root task status = %q, want pending (no scheduler)", tasks[0].Status)
	}

	// Cancel map should be empty (no goroutine registered).
	engine.mu.Lock()
	cancelCount := len(engine.cancels)
	engine.mu.Unlock()
	if cancelCount != 0 {
		t.Errorf("cancel map has %d entries, want 0 (external agents mode)", cancelCount)
	}
}

// --- AgentPoller ---

func TestAgentPoller_StopsOnCancel(t *testing.T) {
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	poller := NewAgentPoller(store, factory, nil, nil, AgentPollerConfig{
		AgentID:            "test-agent",
		PollInterval:       50 * time.Millisecond,
		MaxConcurrentTasks: 2,
		TaskTimeout:        5 * time.Second,
		HeartbeatInterval:  1 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		done <- poller.Run(ctx)
	}()

	// Let it poll a few times.
	time.Sleep(200 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("poller returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("poller did not stop within timeout")
	}
}

func TestAgentPoller_MaybeFinishWorkflow(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	poller := NewAgentPoller(store, factory, nil, nil, AgentPollerConfig{
		AgentID:            "test-agent",
		PollInterval:       50 * time.Millisecond,
		MaxConcurrentTasks: 2,
		TaskTimeout:        5 * time.Second,
		HeartbeatInterval:  1 * time.Second,
	})

	now := time.Now().UTC()
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Status:    WorkflowRunning,
		MaxDepth:  5,
		MaxTasks:  50,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.CreateWorkflow(ctx, wf)

	// Create a completed task.
	task := &Task{
		ID:         uuid.New(),
		WorkflowID: wf.ID,
		AgentRole:  RoleResearcher,
		Status:     TaskCompleted,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	_ = store.CreateTask(ctx, task)

	// Poller should detect workflow is complete.
	poller.maybeFinishWorkflow(ctx, wf)

	got, _ := store.GetWorkflow(ctx, wf.ID)
	if got.Status != WorkflowCompleted {
		t.Errorf("workflow status = %q, want completed", got.Status)
	}
}

func TestAgentPoller_MaybeFinishWorkflow_WithFailures(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	poller := NewAgentPoller(store, factory, nil, nil, AgentPollerConfig{
		AgentID:            "test-agent",
		PollInterval:       50 * time.Millisecond,
		MaxConcurrentTasks: 2,
		TaskTimeout:        5 * time.Second,
		HeartbeatInterval:  1 * time.Second,
	})

	now := time.Now().UTC()
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Status:    WorkflowRunning,
		MaxDepth:  5,
		MaxTasks:  50,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.CreateWorkflow(ctx, wf)

	// Create one completed, one failed task.
	_ = store.CreateTask(ctx, &Task{
		ID: uuid.New(), WorkflowID: wf.ID, AgentRole: RoleResearcher,
		Status: TaskCompleted, CreatedAt: now, UpdatedAt: now,
	})
	_ = store.CreateTask(ctx, &Task{
		ID: uuid.New(), WorkflowID: wf.ID, AgentRole: RoleExecutor,
		Status: TaskFailed, Error: "tool error", CreatedAt: now, UpdatedAt: now,
	})

	poller.maybeFinishWorkflow(ctx, wf)

	got, _ := store.GetWorkflow(ctx, wf.ID)
	if got.Status != WorkflowFailed {
		t.Errorf("workflow status = %q, want failed", got.Status)
	}
}

func TestAgentPoller_MaybeFinishWorkflow_StillRunning(t *testing.T) {
	ctx := context.Background()
	store := NewInMemoryStore()
	factory := &mockAgentFactory{}

	poller := NewAgentPoller(store, factory, nil, nil, AgentPollerConfig{
		AgentID:            "test-agent",
		PollInterval:       50 * time.Millisecond,
		MaxConcurrentTasks: 2,
		TaskTimeout:        5 * time.Second,
		HeartbeatInterval:  1 * time.Second,
	})

	now := time.Now().UTC()
	wf := &Workflow{
		ID:        uuid.New(),
		OrgID:     uuid.New(),
		Status:    WorkflowRunning,
		MaxDepth:  5,
		MaxTasks:  50,
		CreatedAt: now,
		UpdatedAt: now,
	}
	_ = store.CreateWorkflow(ctx, wf)

	// One completed, one still running.
	_ = store.CreateTask(ctx, &Task{
		ID: uuid.New(), WorkflowID: wf.ID, AgentRole: RoleResearcher,
		Status: TaskCompleted, CreatedAt: now, UpdatedAt: now,
	})
	_ = store.CreateTask(ctx, &Task{
		ID: uuid.New(), WorkflowID: wf.ID, AgentRole: RoleExecutor,
		Status: TaskRunning, CreatedAt: now, UpdatedAt: now,
	})

	poller.maybeFinishWorkflow(ctx, wf)

	got, _ := store.GetWorkflow(ctx, wf.ID)
	if got.Status != WorkflowRunning {
		t.Errorf("workflow status = %q, want running (tasks still in progress)", got.Status)
	}
}

// --- Task ClaimedBy/ClaimedAt fields ---

func TestTask_ClaimedFields(t *testing.T) {
	now := time.Now().UTC()
	task := Task{
		ID:        uuid.New(),
		ClaimedBy: "agent-42",
		ClaimedAt: &now,
	}
	if task.ClaimedBy != "agent-42" {
		t.Errorf("ClaimedBy = %q, want agent-42", task.ClaimedBy)
	}
	if task.ClaimedAt == nil || !task.ClaimedAt.Equal(now) {
		t.Errorf("ClaimedAt = %v, want %v", task.ClaimedAt, now)
	}
}

// --- Mock AgentFactory ---

type mockAgentFactory struct{}

func (f *mockAgentFactory) Create(role AgentRole) (RoleAgent, error) {
	return &mockRoleAgent{role: role}, nil
}

type mockRoleAgent struct {
	role AgentRole
}

func (a *mockRoleAgent) Role() AgentRole { return a.role }
func (a *mockRoleAgent) Process(_ context.Context, input *AgentInput) (*AgentOutput, error) {
	// Return a simple plan for planner, plain text for others.
	if a.role == RolePlanner {
		specs := []TaskSpec{
			{AgentRole: RoleResearcher, Description: "research", Input: "gather info"},
		}
		data, _ := json.Marshal(specs)
		return &AgentOutput{Response: string(data)}, nil
	}
	return &AgentOutput{Response: "done: " + input.Instruction}, nil
}
