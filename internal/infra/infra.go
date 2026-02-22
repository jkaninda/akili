// Package infra provides the InfrastructureMemory interface and implementations
// for storing and querying infrastructure node metadata.
package infra

import (
	"context"
	"fmt"

	"github.com/google/uuid"
	"github.com/jkaninda/akili/internal/domain"
)

// Sentinel errors for infrastructure operations.
var (
	ErrNodeNotFound = fmt.Errorf("infrastructure node not found")
	ErrNodeExists   = fmt.Errorf("infrastructure node already exists")
)

// Store is the interface for infrastructure node persistence.
// Implementations must be safe for concurrent use.
type Store interface {
	// Lookup resolves a node by name, alias, or ID (UUID string).
	// Performs case-insensitive matching on name and aliases.
	Lookup(ctx context.Context, orgID uuid.UUID, query string) (*domain.InfraNode, error)

	// List returns all enabled nodes for an organization.
	// If nodeType is non-empty, results are filtered by type.
	List(ctx context.Context, orgID uuid.UUID, nodeType string) ([]domain.InfraNode, error)

	// Create persists a new infrastructure node. Returns ErrNodeExists on duplicate name.
	Create(ctx context.Context, node *domain.InfraNode) error

	// Update persists changes to an existing node.
	Update(ctx context.Context, node *domain.InfraNode) error

	// Delete removes a node by ID within an org.
	Delete(ctx context.Context, orgID, nodeID uuid.UUID) error
}

// SanitizeForLLM returns a map representation of the node with CredentialRef excluded.
// This MUST be used whenever node data is included in LLM-visible output.
func SanitizeForLLM(node *domain.InfraNode) map[string]any {
	return map[string]any{
		"id":         node.ID.String(),
		"name":       node.Name,
		"aliases":    node.Aliases,
		"type":       node.NodeType,
		"host":       node.Host,
		"port":       node.Port,
		"user":       node.User,
		"tags":       node.Tags,
		"mcp_server": node.MCPServer,
		"enabled":    node.Enabled,
		// CredentialRef is intentionally EXCLUDED.
	}
}
