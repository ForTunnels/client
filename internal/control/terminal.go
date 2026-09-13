// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"context"
	"errors"
	"sync"
	"time"

	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

// TerminalCoordinator turns competing lifecycle signals into one CLI outcome.
// The first signal reports the user-facing message, cancels the active
// transport, and closes Done. Later watch, poll, or in-band signals are ignored.
type TerminalCoordinator struct {
	once   sync.Once
	out    Output
	cancel context.CancelFunc
	done   chan struct{}

	mu  sync.RWMutex
	err error
}

// reportedTerminalError marks an error already printed by TerminalCoordinator.
// main uses the marker to preserve a non-zero exit without printing it twice.
type reportedTerminalError struct {
	cause error
}

type tunnelRemovedError struct{}

func (tunnelRemovedError) Error() string { return MsgTunnelRemovedExiting }

func newTunnelRemovedError() error { return tunnelRemovedError{} }

func (e *reportedTerminalError) Error() string { return e.cause.Error() }
func (e *reportedTerminalError) Unwrap() error { return e.cause }

func NewTerminalCoordinator(out Output, cancel context.CancelFunc) *TerminalCoordinator {
	if out == nil {
		out = StdOutput{}
	}
	return &TerminalCoordinator{out: out, cancel: cancel, done: make(chan struct{})}
}

func (c *TerminalCoordinator) Signal(err error) {
	if c == nil {
		return
	}
	if err == nil {
		err = newTunnelRemovedError()
	}
	c.once.Do(func() {
		reported := &reportedTerminalError{cause: err}
		c.mu.Lock()
		c.err = reported
		c.mu.Unlock()
		c.out.Println(err.Error())
		if c.cancel != nil {
			c.cancel()
		}
		close(c.done)
	})
}

func (c *TerminalCoordinator) Done() <-chan struct{} {
	if c == nil {
		return nil
	}
	return c.done
}

func (c *TerminalCoordinator) Err() error {
	if c == nil {
		return nil
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.err
}

// ExitError preserves the successful command outcome for ordinary removal and
// expiry while retaining a non-zero outcome for quota exhaustion.
func (c *TerminalCoordinator) ExitError() error {
	err := c.Err()
	var removed tunnelRemovedError
	if errors.As(err, &removed) {
		return nil
	}
	return err
}

func IsReportedTerminalError(err error) bool {
	var reported *reportedTerminalError
	return errors.As(err, &reported)
}

func TrafficQuotaErrorFromLifecycle(payload protocolv1.LifecycleEventPayload) error {
	if payload.Reason != protocolv1.ReasonMonthlyTraffic {
		return nil
	}
	resetAt := time.Time{}
	if payload.ResetAt != nil {
		resetAt = payload.ResetAt.UTC()
	}
	return &TrafficQuotaError{ResetAt: resetAt}
}
