// Package postgres implements PostgreSQL-backed storage for Akili using GORM.
// All GORM usage is confined to this package — domain types remain ORM-free.
package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Config configures the PostgreSQL connection and pool.
type Config struct {
	DSN             string
	MaxOpenConns    int           // Default: 25
	MaxIdleConns    int           // Default: 5
	ConnMaxLifetime time.Duration // Default: 30m
	ConnMaxIdleTime time.Duration // Default: 10m
}

func (c Config) maxOpen() int {
	if c.MaxOpenConns > 0 {
		return c.MaxOpenConns
	}
	return 25
}

func (c Config) maxIdle() int {
	if c.MaxIdleConns > 0 {
		return c.MaxIdleConns
	}
	return 5
}

func (c Config) maxLifetime() time.Duration {
	if c.ConnMaxLifetime > 0 {
		return c.ConnMaxLifetime
	}
	return 30 * time.Minute
}

func (c Config) maxIdleTime() time.Duration {
	if c.ConnMaxIdleTime > 0 {
		return c.ConnMaxIdleTime
	}
	return 10 * time.Minute
}

// DB wraps a GORM database connection with health check and lifecycle methods.
type DB struct {
	gormDB *gorm.DB
	logger *slog.Logger
}

// Open connects to PostgreSQL, configures the connection pool, and runs AutoMigrate.
func Open(cfg Config, slogger *slog.Logger) (*DB, error) {
	gormLogger := logger.New(
		slogAdapter{slogger},
		logger.Config{
			SlowThreshold:             200 * time.Millisecond,
			LogLevel:                  logger.Warn,
			IgnoreRecordNotFoundError: true,
		},
	)

	db, err := gorm.Open(postgres.Open(cfg.DSN), &gorm.Config{
		Logger:      gormLogger,
		NowFunc:     func() time.Time { return time.Now().UTC() },
		PrepareStmt: true,
	})
	if err != nil {
		return nil, fmt.Errorf("connecting to postgres: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("getting underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxOpenConns(cfg.maxOpen())
	sqlDB.SetMaxIdleConns(cfg.maxIdle())
	sqlDB.SetConnMaxLifetime(cfg.maxLifetime())
	sqlDB.SetConnMaxIdleTime(cfg.maxIdleTime())

	if err := autoMigrate(db); err != nil {
		return nil, fmt.Errorf("auto-migrating: %w", err)
	}

	slogger.Info("postgres connected",
		slog.Int("max_open_conns", cfg.maxOpen()),
		slog.Int("max_idle_conns", cfg.maxIdle()),
	)

	return &DB{gormDB: db, logger: slogger}, nil
}

// GormDB returns the underlying *gorm.DB for repository constructors.
func (d *DB) GormDB() *gorm.DB {
	return d.gormDB
}

// Ping checks the database connection for health/readiness probes.
func (d *DB) Ping(ctx context.Context) error {
	sqlDB, err := d.gormDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close releases the database connection pool.
func (d *DB) Close() error {
	sqlDB, err := d.gormDB.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// SqlDB returns the underlying *sql.DB for raw operations if needed.
func (d *DB) SqlDB() (*sql.DB, error) {
	return d.gormDB.DB()
}

// autoMigrate creates/updates tables in FK-dependency order.
func autoMigrate(db *gorm.DB) error {
	if err := db.AutoMigrate(
		&OrgModel{},
		&UserModel{},
		&RoleModel{},
		&PermissionModel{},
		&UserRoleModel{},
		&BudgetModel{},
		&BudgetReservationModel{},
		&ApprovalModel{},
		&AuditEventModel{},
		&WorkflowModel{},
		&TaskModel{},
		&AgentMessageModel{},
		&AgentSkillModel{},
		&CronJobModel{},
		&ConversationModel{},
		&ConversationMessageModel{},
		&InfraNodeModel{},
		&NotificationChannelModel{},
		&AlertRuleModel{},
		&AlertHistoryModel{},
		&AgentIdentityModel{},
		&AgentHeartbeatModel{},
		&HeartbeatTaskModel{},
		&HeartbeatTaskResultModel{},
		&SoulEventModel{},
	); err != nil {
		return err
	}

	// Ensure indexes that AutoMigrate may not add on pre-existing tables.
	skillRepo := NewSkillRepository(db)
	return skillRepo.EnsureSkillIndex()
}

// slogAdapter wraps *slog.Logger for GORM's logger.Writer interface.
type slogAdapter struct {
	logger *slog.Logger
}

func (s slogAdapter) Printf(format string, args ...any) {
	s.logger.Info(fmt.Sprintf(format, args...))
}
