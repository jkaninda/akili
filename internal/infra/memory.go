package infra

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jkaninda/akili/internal/domain"
)

// MemoryStore is a thread-safe in-memory implementation of Store.
type MemoryStore struct {
	mu    sync.RWMutex
	nodes map[uuid.UUID]*domain.InfraNode
}

// NewMemoryStore creates an empty in-memory infrastructure store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{nodes: make(map[uuid.UUID]*domain.InfraNode)}
}

func (s *MemoryStore) Lookup(_ context.Context, orgID uuid.UUID, query string) (*domain.InfraNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Try UUID parse first.
	if id, err := uuid.Parse(query); err == nil {
		if n, ok := s.nodes[id]; ok && n.OrgID == orgID && n.Enabled {
			return n, nil
		}
	}

	lower := strings.ToLower(query)
	for _, n := range s.nodes {
		if n.OrgID != orgID || !n.Enabled {
			continue
		}
		if strings.ToLower(n.Name) == lower {
			return n, nil
		}
		for _, alias := range n.Aliases {
			if strings.ToLower(alias) == lower {
				return n, nil
			}
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrNodeNotFound, query)
}

func (s *MemoryStore) List(_ context.Context, orgID uuid.UUID, nodeType string) ([]domain.InfraNode, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var result []domain.InfraNode
	for _, n := range s.nodes {
		if n.OrgID != orgID || !n.Enabled {
			continue
		}
		if nodeType != "" && n.NodeType != nodeType {
			continue
		}
		result = append(result, *n)
	}
	return result, nil
}

func (s *MemoryStore) Create(_ context.Context, node *domain.InfraNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Check for duplicate name within org.
	lower := strings.ToLower(node.Name)
	for _, n := range s.nodes {
		if n.OrgID == node.OrgID && strings.ToLower(n.Name) == lower {
			return fmt.Errorf("%w: name %q", ErrNodeExists, node.Name)
		}
	}

	if node.ID == uuid.Nil {
		node.ID = uuid.New()
	}
	now := time.Now().UTC()
	node.CreatedAt = now
	node.UpdatedAt = now

	cp := *node
	s.nodes[node.ID] = &cp
	return nil
}

func (s *MemoryStore) Update(_ context.Context, node *domain.InfraNode) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.nodes[node.ID]
	if !ok || existing.OrgID != node.OrgID {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, node.ID)
	}

	// Check name uniqueness if name changed.
	if strings.ToLower(existing.Name) != strings.ToLower(node.Name) {
		lower := strings.ToLower(node.Name)
		for _, n := range s.nodes {
			if n.ID != node.ID && n.OrgID == node.OrgID && strings.ToLower(n.Name) == lower {
				return fmt.Errorf("%w: name %q", ErrNodeExists, node.Name)
			}
		}
	}

	node.UpdatedAt = time.Now().UTC()
	cp := *node
	s.nodes[node.ID] = &cp
	return nil
}

func (s *MemoryStore) Delete(_ context.Context, orgID, nodeID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	n, ok := s.nodes[nodeID]
	if !ok || n.OrgID != orgID {
		return fmt.Errorf("%w: %s", ErrNodeNotFound, nodeID)
	}
	delete(s.nodes, nodeID)
	return nil
}
