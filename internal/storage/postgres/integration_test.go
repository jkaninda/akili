//go:build integration

package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/approval"
	"github.com/jkaninda/akili/internal/security"
)

func testDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip("TEST_POSTGRES_DSN not set, skipping integration test")
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	db, err := Open(Config{DSN: dsn}, logger)
	if err != nil {
		t.Fatalf("opening postgres: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func testOrg(t *testing.T, db *DB) uuid.UUID {
	t.Helper()
	repo := NewOrgRepository(db.GormDB())
	orgID, err := repo.EnsureDefaultOrg(context.Background(), fmt.Sprintf("test-%s", uuid.New().String()[:8]))
	if err != nil {
		t.Fatalf("creating test org: %v", err)
	}
	return orgID
}

// --- Budget Atomicity ---

func TestBudgetAtomicity_ConcurrentReservations(t *testing.T) {
	db := testDB(t)
	orgID := testOrg(t, db)
	userRepo := NewUserRepository(db.GormDB())
	budgetRepo := NewBudgetRepository(db.GormDB(), userRepo)
	ctx := context.Background()

	// Pre-create user and budget with $10 limit.
	userID, err := userRepo.EnsureUser(ctx, orgID, "alice")
	if err != nil {
		t.Fatalf("creating user: %v", err)
	}
	_ = userID

	// Launch 20 goroutines, each reserving $1 against $10 limit.
	const numWorkers = 20
	const reserveAmount = 1.0
	const budgetLimit = 10.0

	var successCount atomic.Int32
	var failCount atomic.Int32
	var reservationIDs []uuid.UUID
	var mu sync.Mutex

	var wg sync.WaitGroup
	wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go func() {
			defer wg.Done()
			resID, err := budgetRepo.ReserveBudget(ctx, orgID, "alice", reserveAmount, budgetLimit)
			if err != nil {
				failCount.Add(1)
				return
			}
			successCount.Add(1)
			mu.Lock()
			reservationIDs = append(reservationIDs, resID)
			mu.Unlock()
		}()
	}
	wg.Wait()

	if got := successCount.Load(); got != 10 {
		t.Errorf("successful reservations = %d, want 10", got)
	}
	if got := failCount.Load(); got != 10 {
		t.Errorf("failed reservations = %d, want 10", got)
	}

	// Record cost on each successful reservation.
	for _, resID := range reservationIDs {
		if err := budgetRepo.RecordCost(ctx, resID, reserveAmount); err != nil {
			t.Errorf("recording cost for %s: %v", resID, err)
		}
	}

	// Verify remaining budget is $0.
	remaining, err := budgetRepo.CheckBudget(ctx, orgID, "alice", budgetLimit)
	if err != nil {
		t.Fatalf("checking budget: %v", err)
	}
	if remaining != 0 {
		t.Errorf("remaining = %.2f, want 0", remaining)
	}
}

// --- Approval State Machine ---

func TestApprovalStateMachine(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	orgID := testOrg(t, db)
	repo := NewApprovalRepository(db.GormDB())

	t.Run("Pending→Approved", func(t *testing.T) {
		id, err := repo.Create(ctx, orgID, &approval.CreateRequest{
			UserID:   "alice",
			ToolName: "shell_exec",
		}, 5*time.Minute)
		if err != nil {
			t.Fatalf("creating approval: %v", err)
		}

		if err := repo.Approve(ctx, id, "bob"); err != nil {
			t.Fatalf("approving: %v", err)
		}

		pa, err := repo.Get(ctx, id)
		if err != nil {
			t.Fatalf("getting: %v", err)
		}
		if pa.Status != approval.StatusApproved {
			t.Errorf("status = %v, want Approved", pa.Status)
		}
		if pa.ApprovedBy != "bob" {
			t.Errorf("approved_by = %q, want %q", pa.ApprovedBy, "bob")
		}

		// Second approve should fail.
		if err := repo.Approve(ctx, id, "carol"); err != approval.ErrAlreadyResolved {
			t.Errorf("second approve err = %v, want ErrAlreadyResolved", err)
		}
	})

	t.Run("Pending→Denied", func(t *testing.T) {
		id, err := repo.Create(ctx, orgID, &approval.CreateRequest{
			UserID:   "alice",
			ToolName: "shell_exec",
		}, 5*time.Minute)
		if err != nil {
			t.Fatalf("creating: %v", err)
		}

		if err := repo.Deny(ctx, id, "bob"); err != nil {
			t.Fatalf("denying: %v", err)
		}

		pa, err := repo.Get(ctx, id)
		if err != nil {
			t.Fatalf("getting: %v", err)
		}
		if pa.Status != approval.StatusDenied {
			t.Errorf("status = %v, want Denied", pa.Status)
		}

		// Cannot approve after deny.
		if err := repo.Approve(ctx, id, "carol"); err != approval.ErrAlreadyResolved {
			t.Errorf("approve after deny err = %v, want ErrAlreadyResolved", err)
		}
	})

	t.Run("Expired", func(t *testing.T) {
		id, err := repo.Create(ctx, orgID, &approval.CreateRequest{
			UserID:   "alice",
			ToolName: "shell_exec",
		}, 1*time.Millisecond)
		if err != nil {
			t.Fatalf("creating: %v", err)
		}

		time.Sleep(5 * time.Millisecond)

		pa, err := repo.Get(ctx, id)
		if err != nil {
			t.Fatalf("getting: %v", err)
		}
		if pa.Status != approval.StatusExpired {
			t.Errorf("status = %v, want Expired", pa.Status)
		}

		// Cannot approve expired.
		if err := repo.Approve(ctx, id, "bob"); err != approval.ErrAlreadyResolved {
			t.Errorf("approve expired err = %v, want ErrAlreadyResolved", err)
		}
	})
}

// --- Multi-Tenant Isolation ---

func TestMultiTenantIsolation(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()

	orgRepo := NewOrgRepository(db.GormDB())
	orgA, _ := orgRepo.EnsureDefaultOrg(ctx, fmt.Sprintf("org-a-%s", uuid.New().String()[:8]))
	orgB, _ := orgRepo.EnsureDefaultOrg(ctx, fmt.Sprintf("org-b-%s", uuid.New().String()[:8]))

	userRepo := NewUserRepository(db.GormDB())
	auditRepo := NewAuditRepository(db.GormDB())
	approvalRepo := NewApprovalRepository(db.GormDB())

	t.Run("AuditIsolation", func(t *testing.T) {
		// Write audit event to org A.
		err := auditRepo.Append(ctx, orgA, security.AuditEvent{
			Timestamp: time.Now().UTC(),
			UserID:    "alice",
			Action:    "test",
			Tool:      "test",
			Result:    "success",
		})
		if err != nil {
			t.Fatalf("appending: %v", err)
		}

		// Query from org B should return nothing.
		events, err := auditRepo.Query(ctx, orgB, "", 100)
		if err != nil {
			t.Fatalf("querying: %v", err)
		}
		if len(events) != 0 {
			t.Errorf("got %d events from org B, want 0", len(events))
		}
	})

	t.Run("BudgetIsolation", func(t *testing.T) {
		budgetRepo := NewBudgetRepository(db.GormDB(), userRepo)

		// Create budget for alice in org A with $10 limit.
		_, err := budgetRepo.ReserveBudget(ctx, orgA, "alice", 5.0, 10.0)
		if err != nil {
			t.Fatalf("reserving in org A: %v", err)
		}

		// alice in org B should have full budget ($10).
		remaining, err := budgetRepo.CheckBudget(ctx, orgB, "alice", 10.0)
		if err != nil {
			t.Fatalf("checking budget in org B: %v", err)
		}
		if remaining != 10.0 {
			t.Errorf("org B remaining = %.2f, want 10.00", remaining)
		}
	})

	t.Run("ApprovalIsolation", func(t *testing.T) {
		// Create approval in org A.
		id, err := approvalRepo.Create(ctx, orgA, &approval.CreateRequest{
			UserID:   "alice",
			ToolName: "shell_exec",
		}, 5*time.Minute)
		if err != nil {
			t.Fatalf("creating: %v", err)
		}

		// Should be retrievable (approvals are not org-scoped on Get by ID for now,
		// but the org_id column enables future scoping).
		pa, err := approvalRepo.Get(ctx, id)
		if err != nil {
			t.Fatalf("getting from org A: %v", err)
		}
		if pa.UserID != "alice" {
			t.Errorf("user_id = %q, want alice", pa.UserID)
		}
	})
}

// --- Audit Immutability ---

func TestAuditAppendOnly(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	orgID := testOrg(t, db)
	repo := NewAuditRepository(db.GormDB())

	// Insert events.
	for i := 0; i < 5; i++ {
		err := repo.Append(ctx, orgID, security.AuditEvent{
			Timestamp:     time.Now().UTC(),
			CorrelationID: fmt.Sprintf("corr-%d", i),
			UserID:        "alice",
			Action:        "test_action",
			Tool:          "test_tool",
			Result:        "success",
		})
		if err != nil {
			t.Fatalf("appending event %d: %v", i, err)
		}
	}

	// Query should return newest first.
	events, err := repo.Query(ctx, orgID, "alice", 10)
	if err != nil {
		t.Fatalf("querying: %v", err)
	}
	if len(events) != 5 {
		t.Fatalf("got %d events, want 5", len(events))
	}

	// Verify ordering (newest first).
	for i := 1; i < len(events); i++ {
		if events[i].Timestamp.After(events[i-1].Timestamp) {
			t.Errorf("event %d timestamp %v is after event %d timestamp %v (should be newest first)",
				i, events[i].Timestamp, i-1, events[i-1].Timestamp)
		}
	}
}

// --- Connection Health ---

func TestConnectionHealth(t *testing.T) {
	db := testDB(t)
	if err := db.Ping(context.Background()); err != nil {
		t.Fatalf("ping failed: %v", err)
	}
}

// --- Role Repository ---

func TestRoleRepository_SaveAndLoad(t *testing.T) {
	db := testDB(t)
	ctx := context.Background()
	orgID := testOrg(t, db)

	userRepo := NewUserRepository(db.GormDB())
	roleRepo := NewRoleRepository(db.GormDB(), userRepo)

	// Save a role.
	role := security.Role{
		Name:            "admin",
		Permissions:     []string{"read_files", "shell_exec", "code_exec"},
		MaxRiskLevel:    "high",
		RequireApproval: []string{"code_exec"},
	}
	if err := roleRepo.SaveRole(ctx, orgID, role); err != nil {
		t.Fatalf("saving role: %v", err)
	}

	// Assign a user.
	if err := roleRepo.AssignUserRole(ctx, orgID, "alice", "admin"); err != nil {
		t.Fatalf("assigning role: %v", err)
	}

	// Load config and verify.
	cfg, err := roleRepo.LoadRBACConfig(ctx, orgID)
	if err != nil {
		t.Fatalf("loading config: %v", err)
	}

	loaded, ok := cfg.Roles["admin"]
	if !ok {
		t.Fatal("admin role not found in loaded config")
	}
	if len(loaded.Permissions) != 3 {
		t.Errorf("permissions count = %d, want 3", len(loaded.Permissions))
	}
	if loaded.MaxRiskLevel != "high" {
		t.Errorf("max_risk_level = %q, want %q", loaded.MaxRiskLevel, "high")
	}
	if len(loaded.RequireApproval) != 1 || loaded.RequireApproval[0] != "code_exec" {
		t.Errorf("require_approval = %v, want [code_exec]", loaded.RequireApproval)
	}
	if cfg.UserRoles["alice"] != "admin" {
		t.Errorf("user_roles[alice] = %q, want %q", cfg.UserRoles["alice"], "admin")
	}
}
