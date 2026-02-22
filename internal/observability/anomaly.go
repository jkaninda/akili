package observability

import (
	"log/slog"
	"sync"
	"time"

	"github.com/jkaninda/akili/internal/config"
)

// AnomalyDetector performs threshold-based anomaly detection using sliding windows.
type AnomalyDetector struct {
	mu            sync.Mutex
	errorCounts   map[string]*slidingWindow
	successCounts map[string]*slidingWindow
	budgetSpend   map[string]*slidingWindow
	cfg           *config.AnomalyConfig
	logger        *slog.Logger
}

type slidingWindow struct {
	entries []windowEntry
	window  time.Duration
}

type windowEntry struct {
	timestamp time.Time
	value     float64
}

// NewAnomalyDetector creates an anomaly detector from config.
func NewAnomalyDetector(cfg *config.AnomalyConfig, logger *slog.Logger) *AnomalyDetector {
	windowSecs := cfg.WindowSeconds
	if windowSecs <= 0 {
		windowSecs = 300
	}

	return &AnomalyDetector{
		errorCounts:   make(map[string]*slidingWindow),
		successCounts: make(map[string]*slidingWindow),
		budgetSpend:   make(map[string]*slidingWindow),
		cfg:           cfg,
		logger:        logger,
	}
}

func (a *AnomalyDetector) windowDuration() time.Duration {
	secs := a.cfg.WindowSeconds
	if secs <= 0 {
		secs = 300
	}
	return time.Duration(secs) * time.Second
}

// RecordError records a failed operation for anomaly tracking.
func (a *AnomalyDetector) RecordError(operation string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	w := a.getOrCreateWindow(a.errorCounts, operation)
	w.add(1)
	a.checkErrorRate(operation)
}

// RecordSuccess records a successful operation.
func (a *AnomalyDetector) RecordSuccess(operation string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	w := a.getOrCreateWindow(a.successCounts, operation)
	w.add(1)
}

// RecordBudgetSpend records budget spend for a user.
func (a *AnomalyDetector) RecordBudgetSpend(userID string, amount float64) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()

	w := a.getOrCreateWindow(a.budgetSpend, userID)
	w.add(amount)
}

// checkErrorRate checks if the error rate exceeds the configured threshold.
// Must be called with a.mu held.
func (a *AnomalyDetector) checkErrorRate(operation string) {
	threshold := a.cfg.ErrorRateThreshold
	if threshold <= 0 {
		return
	}

	errors := a.getOrCreateWindow(a.errorCounts, operation).sum()
	successes := a.getOrCreateWindow(a.successCounts, operation).sum()
	total := errors + successes

	if total < 5 {
		return // Not enough data.
	}

	rate := errors / total
	if rate > threshold && a.logger != nil {
		a.logger.Warn("anomaly detected: high error rate",
			slog.String("operation", operation),
			slog.Float64("error_rate", rate),
			slog.Float64("threshold", threshold),
			slog.Float64("errors", errors),
			slog.Float64("total", total),
		)
	}
}

func (a *AnomalyDetector) getOrCreateWindow(m map[string]*slidingWindow, key string) *slidingWindow {
	w, ok := m[key]
	if !ok {
		w = &slidingWindow{window: a.windowDuration()}
		m[key] = w
	}
	return w
}

// add appends a value and prunes expired entries.
func (w *slidingWindow) add(value float64) {
	now := time.Now()
	w.entries = append(w.entries, windowEntry{timestamp: now, value: value})
	w.prune(now)
}

// sum returns the total value within the window.
func (w *slidingWindow) sum() float64 {
	w.prune(time.Now())
	var total float64
	for _, e := range w.entries {
		total += e.value
	}
	return total
}

// prune removes entries older than the window duration.
func (w *slidingWindow) prune(now time.Time) {
	cutoff := now.Add(-w.window)
	i := 0
	for i < len(w.entries) && w.entries[i].timestamp.Before(cutoff) {
		i++
	}
	if i > 0 {
		w.entries = w.entries[i:]
	}
}
