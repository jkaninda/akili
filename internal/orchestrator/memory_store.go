package orchestrator

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"
)

// InMemoryStore implements WorkflowStore using in-memory maps.
// Used when PostgreSQL is not configured.
type InMemoryStore struct {
	mu        sync.RWMutex
	workflows map[uuid.UUID]*Workflow
	tasks     map[uuid.UUID]*Task
	messages  map[uuid.UUID]*AgentMessage
}

// NewInMemoryStore creates an empty in-memory workflow store.
func NewInMemoryStore() *InMemoryStore {
	return &InMemoryStore{
		workflows: make(map[uuid.UUID]*Workflow),
		tasks:     make(map[uuid.UUID]*Task),
		messages:  make(map[uuid.UUID]*AgentMessage),
	}
}

func (s *InMemoryStore) CreateWorkflow(_ context.Context, wf *Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.workflows[wf.ID]; exists {
		return fmt.Errorf("workflow %s already exists", wf.ID)
	}
	cp := *wf
	s.workflows[wf.ID] = &cp
	return nil
}

func (s *InMemoryStore) UpdateWorkflow(_ context.Context, wf *Workflow) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.workflows[wf.ID]; !exists {
		return fmt.Errorf("workflow %s not found", wf.ID)
	}
	cp := *wf
	cp.UpdatedAt = time.Now().UTC()
	s.workflows[wf.ID] = &cp
	return nil
}

func (s *InMemoryStore) GetWorkflow(_ context.Context, id uuid.UUID) (*Workflow, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	wf, ok := s.workflows[id]
	if !ok {
		return nil, fmt.Errorf("workflow %s not found", id)
	}
	cp := *wf
	return &cp, nil
}

func (s *InMemoryStore) CreateTask(_ context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[task.ID]; exists {
		return fmt.Errorf("task %s already exists", task.ID)
	}
	cp := *task
	// Deep-copy DependsOn slice.
	if task.DependsOn != nil {
		cp.DependsOn = make([]uuid.UUID, len(task.DependsOn))
		copy(cp.DependsOn, task.DependsOn)
	}
	s.tasks[task.ID] = &cp
	return nil
}

func (s *InMemoryStore) UpdateTask(_ context.Context, task *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tasks[task.ID]; !exists {
		return fmt.Errorf("task %s not found", task.ID)
	}
	cp := *task
	cp.UpdatedAt = time.Now().UTC()
	if task.DependsOn != nil {
		cp.DependsOn = make([]uuid.UUID, len(task.DependsOn))
		copy(cp.DependsOn, task.DependsOn)
	}
	s.tasks[task.ID] = &cp
	return nil
}

func (s *InMemoryStore) GetTask(_ context.Context, id uuid.UUID) (*Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	task, ok := s.tasks[id]
	if !ok {
		return nil, fmt.Errorf("task %s not found", id)
	}
	cp := *task
	return &cp, nil
}

func (s *InMemoryStore) ListTasksByWorkflow(_ context.Context, workflowID uuid.UUID) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []Task
	for _, t := range s.tasks {
		if t.WorkflowID == workflowID {
			cp := *t
			result = append(result, cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (s *InMemoryStore) GetReadyTasks(_ context.Context, workflowID uuid.UUID) ([]Task, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Build lookup of completed task IDs for this workflow.
	completed := make(map[uuid.UUID]bool)
	var candidates []*Task
	for _, t := range s.tasks {
		if t.WorkflowID != workflowID {
			continue
		}
		if t.Status == TaskCompleted {
			completed[t.ID] = true
		}
		if t.Status == TaskPending || t.Status == TaskBlocked {
			candidates = append(candidates, t)
		}
	}

	var ready []Task
	for _, t := range candidates {
		allDepsMet := true
		for _, depID := range t.DependsOn {
			if !completed[depID] {
				allDepsMet = false
				break
			}
		}
		if allDepsMet {
			cp := *t
			ready = append(ready, cp)
		}
	}

	// Sort by priority (lower = higher priority), then creation time.
	sort.Slice(ready, func(i, j int) bool {
		if ready[i].Priority != ready[j].Priority {
			return ready[i].Priority < ready[j].Priority
		}
		return ready[i].CreatedAt.Before(ready[j].CreatedAt)
	})

	return ready, nil
}

func (s *InMemoryStore) SaveMessage(_ context.Context, msg *AgentMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *msg
	s.messages[msg.ID] = &cp
	return nil
}

func (s *InMemoryStore) ListMessages(_ context.Context, workflowID uuid.UUID) ([]AgentMessage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var result []AgentMessage
	for _, m := range s.messages {
		if m.WorkflowID == workflowID {
			cp := *m
			result = append(result, cp)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].CreatedAt.Before(result[j].CreatedAt)
	})
	return result, nil
}

func (s *InMemoryStore) ClaimReadyTasks(_ context.Context, _ string, _ []string, _ int) ([]Task, error) {
	return nil, fmt.Errorf("ClaimReadyTasks is not supported in in-memory mode; agent mode requires postgres")
}

func (s *InMemoryStore) HeartbeatTasks(_ context.Context, _ string, _ []uuid.UUID) error {
	return fmt.Errorf("HeartbeatTasks is not supported in in-memory mode; agent mode requires postgres")
}

// --- InMemorySkillStore ---

// InMemorySkillStore implements SkillStore using in-memory maps.
type InMemorySkillStore struct {
	mu     sync.RWMutex
	skills map[string]*AgentSkill // key = "orgID:role:skillKey"
}

// NewInMemorySkillStore creates an empty in-memory skill store.
func NewInMemorySkillStore() *InMemorySkillStore {
	return &InMemorySkillStore{
		skills: make(map[string]*AgentSkill),
	}
}

func skillMapKey(orgID uuid.UUID, role AgentRole, skillKey string) string {
	return orgID.String() + ":" + string(role) + ":" + skillKey
}

func (s *InMemorySkillStore) GetSkill(_ context.Context, orgID uuid.UUID, agentRole AgentRole, skillKey string) (*AgentSkill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	key := skillMapKey(orgID, agentRole, skillKey)
	skill, ok := s.skills[key]
	if !ok {
		return nil, fmt.Errorf("skill %s not found", key)
	}
	cp := *skill
	return &cp, nil
}

func (s *InMemorySkillStore) UpsertSkill(_ context.Context, skill *AgentSkill) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := skillMapKey(skill.OrgID, skill.AgentRole, skill.SkillKey)
	cp := *skill
	s.skills[key] = &cp
	return nil
}

func (s *InMemorySkillStore) ListSkillsByRole(_ context.Context, orgID uuid.UUID, agentRole AgentRole) ([]AgentSkill, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	prefix := orgID.String() + ":" + string(agentRole) + ":"
	var result []AgentSkill
	for k, v := range s.skills {
		if len(k) >= len(prefix) && k[:len(prefix)] == prefix {
			cp := *v
			result = append(result, cp)
		}
	}
	return result, nil
}

// Compile-time checks.
var _ WorkflowStore = (*InMemoryStore)(nil)
var _ SkillStore = (*InMemorySkillStore)(nil)
