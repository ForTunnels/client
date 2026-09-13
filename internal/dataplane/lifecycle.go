// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

type LifecycleHandler func(protocolv1.LifecycleEventPayload)

func openLifecycleControlStream(sess dataPlaneSession, handler LifecycleHandler) (io.Closer, error) {
	stream, err := sess.OpenStream()
	if err != nil {
		return nil, fmt.Errorf("open lifecycle control stream: %w", err)
	}
	preface, err := encodePreface(map[string]string{"proto": "control"})
	if err != nil {
		_ = stream.Close()
		return nil, err
	}
	if _, err := stream.Write(preface); err != nil {
		_ = stream.Close()
		return nil, fmt.Errorf("write lifecycle control preface: %w", err)
	}
	go readLifecycleControlStream(stream, handler)
	return stream, nil
}

func readLifecycleControlStream(reader io.Reader, handler LifecycleHandler) {
	decoder := json.NewDecoder(reader)
	for {
		var event struct {
			Event    string     `json:"event"`
			TunnelID string     `json:"tunnel_id"`
			Status   string     `json:"status,omitempty"`
			Reason   string     `json:"reason,omitempty"`
			ResetAt  *time.Time `json:"reset_at,omitempty"`
		}
		if err := decoder.Decode(&event); err != nil {
			return
		}
		if event.Event != protocolv1.EventTunnelClosed && event.Status != protocolv1.StatusExpired {
			continue
		}
		if handler != nil {
			handler(protocolv1.LifecycleEventPayload{
				TunnelID: event.TunnelID,
				Status:   event.Status,
				Reason:   event.Reason,
				ResetAt:  event.ResetAt,
			})
		}
		return
	}
}
