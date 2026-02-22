// Package identity provides agent identity management with Ed25519 keypairs.
// Each agent has a persistent, verifiable identity used for secure communication,
// gateway registration, and heartbeat signing.
package identity

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
)

// TrustLevel classifies how much the gateway trusts an agent.
type TrustLevel string

const (
	TrustUntrusted TrustLevel = "untrusted" // New agent, not yet verified.
	TrustBasic     TrustLevel = "basic"     // Registered, identity verified.
	TrustElevated  TrustLevel = "elevated"  // Proven track record.
	TrustFull      TrustLevel = "full"      // Fully trusted.
)

// IdentityConfig is the complete agent identity.
type IdentityConfig struct {
	AgentID          string             // Unique persistent identifier (UUID).
	Name             string             // Human-readable name.
	Version          string             // Agent software version.
	PublicKey        ed25519.PublicKey   // For verifiable signing.
	PrivateKey       ed25519.PrivateKey // For signing messages (never leaves agent).
	Capabilities     []string           // Registered capabilities (tool names, roles).
	TrustLevel       TrustLevel         // Assigned by gateway.
	EnvironmentScope string             // "production", "staging", "development".
	RegisteredAt     time.Time
}

// NewIdentity generates a fresh identity with a new Ed25519 keypair.
func NewIdentity(name, version, envScope string, capabilities []string) (*IdentityConfig, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generating keypair: %w", err)
	}
	return &IdentityConfig{
		AgentID:          uuid.New().String(),
		Name:             name,
		Version:          version,
		PublicKey:        pub,
		PrivateKey:       priv,
		Capabilities:     capabilities,
		TrustLevel:       TrustUntrusted,
		EnvironmentScope: envScope,
		RegisteredAt:     time.Now().UTC(),
	}, nil
}

// Sign produces an Ed25519 signature over the given payload.
func (id *IdentityConfig) Sign(data []byte) []byte {
	return ed25519.Sign(id.PrivateKey, data)
}

// Verify checks a signature against this identity's public key.
func (id *IdentityConfig) Verify(data, signature []byte) bool {
	return ed25519.Verify(id.PublicKey, data, signature)
}

// PublicKeyHex returns the hex-encoded public key for gateway registration.
func (id *IdentityConfig) PublicKeyHex() string {
	return hex.EncodeToString(id.PublicKey)
}

// SavePrivateKey persists the private key to a PEM file.
func (id *IdentityConfig) SavePrivateKey(path string) error {
	block := &pem.Block{
		Type:  "ED25519 PRIVATE KEY",
		Bytes: id.PrivateKey.Seed(),
	}
	data := pem.EncodeToMemory(block)
	return os.WriteFile(path, data, 0600)
}

// LoadIdentity loads an identity from a PEM key file, deriving the public key.
// The agentID, name, version, and other metadata are passed in separately
// (they come from config, not the key file).
func LoadIdentity(keyFile, agentID, name, version, envScope string, capabilities []string) (*IdentityConfig, error) {
	data, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("reading key file: %w", err)
	}

	block, _ := pem.Decode(data)
	if block == nil {
		return nil, fmt.Errorf("no PEM block found in %s", keyFile)
	}
	if block.Type != "ED25519 PRIVATE KEY" {
		return nil, fmt.Errorf("unexpected PEM type %q, expected ED25519 PRIVATE KEY", block.Type)
	}

	if len(block.Bytes) != ed25519.SeedSize {
		return nil, fmt.Errorf("invalid key seed length: got %d, want %d", len(block.Bytes), ed25519.SeedSize)
	}

	priv := ed25519.NewKeyFromSeed(block.Bytes)
	pub := priv.Public().(ed25519.PublicKey)

	return &IdentityConfig{
		AgentID:          agentID,
		Name:             name,
		Version:          version,
		PublicKey:        pub,
		PrivateKey:       priv,
		Capabilities:     capabilities,
		TrustLevel:       TrustUntrusted,
		EnvironmentScope: envScope,
		RegisteredAt:     time.Now().UTC(),
	}, nil
}

// IdentityStore persists agent identities for gateway-side verification.
type IdentityStore interface {
	Register(ctx context.Context, id *IdentityConfig) error
	GetByAgentID(ctx context.Context, agentID string) (*IdentityConfig, error)
	UpdateTrustLevel(ctx context.Context, agentID string, level TrustLevel) error
	UpdateStatus(ctx context.Context, agentID string, status string, heartbeatAt time.Time) error
	ListByEnvironment(ctx context.Context, envScope string) ([]IdentityConfig, error)
	Remove(ctx context.Context, agentID string) error
}
