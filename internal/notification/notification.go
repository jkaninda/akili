// Package notification implements the notification dispatcher for Akili.
// It sends alert messages through configured channels (Telegram, Slack, Email, Webhook)
// with fallback logic and audit logging.
//
// Security: every notification attempt is audit-logged via SecurityManager.
// Credentials are resolved per-channel via CredentialRef, never stored in memory.
package notification

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/jkaninda/akili/internal/domain"
	"github.com/jkaninda/akili/internal/security"
)

// Sender is the interface for a single notification channel backend.
type Sender interface {
	// Type returns the channel type identifier ("telegram", "slack", "email", "webhook").
	Type() string
	// Send delivers a message to the target specified by the channel config.
	Send(ctx context.Context, channel *domain.NotificationChannel, msg *Message) error
}

// Message is the payload to be sent through a notification channel.
type Message struct {
	Subject  string            // Used by email; ignored by chat channels.
	Body     string            // Plain text body.
	Metadata map[string]string // Extra data (alert_rule_id, status, etc.).
}

// ChannelStore provides notification channel persistence.
type ChannelStore interface {
	Get(ctx context.Context, orgID, id uuid.UUID) (*domain.NotificationChannel, error)
	GetByName(ctx context.Context, orgID uuid.UUID, name string) (*domain.NotificationChannel, error)
	List(ctx context.Context, orgID uuid.UUID) ([]domain.NotificationChannel, error)
	Create(ctx context.Context, ch *domain.NotificationChannel) error
	Update(ctx context.Context, ch *domain.NotificationChannel) error
	Delete(ctx context.Context, orgID, id uuid.UUID) error
}

// Dispatcher routes notifications to the appropriate Sender based on channel type.
// Thread-safe. Supports fallback: if the primary channel fails, tries alternatives.
type Dispatcher struct {
	senders  map[string]Sender
	store    ChannelStore
	security security.SecurityManager
	logger   *slog.Logger
	mu       sync.RWMutex
}

// NewDispatcher creates a notification dispatcher.
func NewDispatcher(store ChannelStore, sec security.SecurityManager, logger *slog.Logger) *Dispatcher {
	return &Dispatcher{
		senders:  make(map[string]Sender),
		store:    store,
		security: sec,
		logger:   logger,
	}
}

// RegisterSender adds a channel backend. Not thread-safe — call at startup only.
func (d *Dispatcher) RegisterSender(s Sender) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.senders[s.Type()] = s
}

// Notify sends a message to all specified channels. Returns per-channel errors (nil = success).
func (d *Dispatcher) Notify(ctx context.Context, orgID uuid.UUID, channelIDs []uuid.UUID, msg *Message, userID string) map[uuid.UUID]error {
	errors := make(map[uuid.UUID]error, len(channelIDs))

	for _, chID := range channelIDs {
		ch, err := d.store.Get(ctx, orgID, chID)
		if err != nil {
			errors[chID] = fmt.Errorf("channel lookup: %w", err)
			d.auditNotify(ctx, userID, chID.String(), "", "failure", err.Error())
			continue
		}
		if !ch.Enabled {
			errors[chID] = fmt.Errorf("channel %q is disabled", ch.Name)
			continue
		}

		d.mu.RLock()
		sender, ok := d.senders[ch.ChannelType]
		d.mu.RUnlock()
		if !ok {
			errors[chID] = fmt.Errorf("no sender registered for channel type %q", ch.ChannelType)
			d.auditNotify(ctx, userID, ch.Name, ch.ChannelType, "failure", "no sender")
			continue
		}

		if err := sender.Send(ctx, ch, msg); err != nil {
			errors[chID] = err
			d.auditNotify(ctx, userID, ch.Name, ch.ChannelType, "failure", err.Error())
			d.logger.WarnContext(ctx, "notification send failed",
				slog.String("channel", ch.Name),
				slog.String("type", ch.ChannelType),
				slog.String("error", err.Error()),
			)
		} else {
			errors[chID] = nil
			d.auditNotify(ctx, userID, ch.Name, ch.ChannelType, "success", "")
			d.logger.InfoContext(ctx, "notification sent",
				slog.String("channel", ch.Name),
				slog.String("type", ch.ChannelType),
			)
		}
	}

	return errors
}

// NotifyWithFallback tries channels in order; stops at first success.
// Returns nil on first success, or an aggregate error if all fail.
func (d *Dispatcher) NotifyWithFallback(ctx context.Context, orgID uuid.UUID, channelIDs []uuid.UUID, msg *Message, userID string) error {
	var lastErr error
	for _, chID := range channelIDs {
		ch, err := d.store.Get(ctx, orgID, chID)
		if err != nil {
			lastErr = fmt.Errorf("channel %s lookup: %w", chID, err)
			continue
		}
		if !ch.Enabled {
			lastErr = fmt.Errorf("channel %q is disabled", ch.Name)
			continue
		}

		d.mu.RLock()
		sender, ok := d.senders[ch.ChannelType]
		d.mu.RUnlock()
		if !ok {
			lastErr = fmt.Errorf("no sender for type %q", ch.ChannelType)
			continue
		}

		if err := sender.Send(ctx, ch, msg); err != nil {
			lastErr = err
			d.logger.WarnContext(ctx, "fallback: notification failed, trying next",
				slog.String("channel", ch.Name),
				slog.String("error", err.Error()),
			)
			continue
		}

		d.auditNotify(ctx, userID, ch.Name, ch.ChannelType, "success", "")
		return nil
	}
	return fmt.Errorf("all notification channels failed, last error: %w", lastErr)
}

// Store returns the underlying ChannelStore for direct access by tools/API.
func (d *Dispatcher) Store() ChannelStore {
	return d.store
}

func (d *Dispatcher) auditNotify(ctx context.Context, userID, channelName, channelType, result, errMsg string) {
	if d.security == nil {
		return
	}
	_ = d.security.LogAction(ctx, security.AuditEvent{
		Timestamp: time.Now().UTC(),
		UserID:    userID,
		Action:    "notification.send",
		Tool:      "notification",
		Parameters: map[string]any{
			"channel_name": channelName,
			"channel_type": channelType,
		},
		Result: result,
		Error:  errMsg,
	})
}
