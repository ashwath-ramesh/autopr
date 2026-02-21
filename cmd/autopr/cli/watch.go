package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"
)

const defaultWatchInterval = 5 * time.Second

func runWatchLoop(ctx context.Context, interval time.Duration, fn func(context.Context, int64) error) error {
	if interval <= 0 {
		return fmt.Errorf("interval must be > 0; got %v", interval)
	}

	watchCtx, stop := signal.NotifyContext(ctx, os.Interrupt)
	defer stop()

	select {
	case <-watchCtx.Done():
		return nil
	default:
	}

	if err := fn(watchCtx, 0); err != nil {
		return normalizeWatchError(err)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for i := int64(1); ; i++ {
		select {
		case <-watchCtx.Done():
			return nil
		case <-ticker.C:
			if err := fn(watchCtx, i); err != nil {
				return normalizeWatchError(err)
			}
		}
	}
}

func normalizeWatchError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return nil
	}
	return err
}

func writef(format string, args ...any) error {
	if _, err := fmt.Fprintf(os.Stdout, format, args...); err != nil {
		return fmt.Errorf("write output: %w", err)
	}
	return nil
}

func writeJSONLine(v any) error {
	enc := json.NewEncoder(os.Stdout)
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("encode json: %w", err)
	}
	return nil
}
