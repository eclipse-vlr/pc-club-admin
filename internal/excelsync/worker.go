package excelsync

import (
	"context"
	"errors"
	"log/slog"
	"time"
)

// Run owns one cancellation-aware loop, one file operation at a time, at most
// once per five seconds. A repository-level advisory lock covers multiple API
// processes; conditional writes also protect against human and remote writers.
func Run(ctx context.Context, repo Repository, target string, deliver func(context.Context, Record) error, logger *slog.Logger) {
	for {
		if ctx.Err() != nil {
			return
		}
		cycle, cancel := context.WithTimeout(ctx, 45*time.Second)
		_, err := repo.DeliverOne(cycle, target, deliver)
		cancel()
		if err != nil && ctx.Err() == nil {
			logger.Warn("excel synchronization cycle unavailable", "code", Code(err))
		}
		timer := time.NewTimer(5 * time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func Code(err error) string {
	var failure *Failure
	if errors.As(err, &failure) {
		return failure.Code
	}
	return "EXCEL_SYNC_UNAVAILABLE"
}

// RetryDelay bounds retry traffic; permanent configuration failures are checked
// periodically so a corrected Drive permission recovers without a new shift.
func RetryDelay(err error, attempts int) time.Duration {
	var failure *Failure
	if !errors.As(err, &failure) || !failure.Temporary {
		return 5 * time.Minute
	}
	if attempts < 0 {
		attempts = 0
	}
	if attempts > 6 {
		attempts = 6
	}
	delay := 5 * time.Second * time.Duration(1<<attempts)
	if delay > 5*time.Minute {
		delay = 5 * time.Minute
	}
	return delay
}
