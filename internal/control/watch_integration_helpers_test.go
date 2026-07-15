//go:build integration

// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import (
	"encoding/json"
	"net/http"
	"sync"
	"time"

	protocolv1 "github.com/fortunnels/client/shared/protocol/v1"
)

func checkTunnelTerminalWithStatus(client *http.Client, serverURL, tunnelID, bearer string) (terminal bool, statusCode int) {
	terminal, _, statusCode = checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
	return terminal, statusCode
}

func checkTunnelTerminal(client *http.Client, serverURL, tunnelID, bearer string) bool {
	terminal, _, _ := checkTunnelTerminalWithStatusImpl(client, serverURL, tunnelID, bearer)
	return terminal
}

func checkTunnelDeleted(client *http.Client, serverURL, tunnelID string) bool {
	return checkTunnelTerminal(client, serverURL, tunnelID, "")
}

func handleControlMessage(
	msg map[string]interface{},
	ackCh chan<- struct{},
	intervalCh chan<- time.Duration,
	done chan struct{},
	doneOnce *sync.Once,
	defaultWatchInterval time.Duration,
	lastStatus *string,
) bool {
	return NewWatcher(nil).handleControlMessage(
		envelopeFromMap(msg),
		ackCh,
		intervalCh,
		done,
		doneOnce,
		defaultWatchInterval,
		lastStatus,
	)
}

func extractPayload(msg map[string]interface{}) map[string]interface{} {
	payload, ok := msg["payload"].(map[string]interface{})
	if !ok {
		return nil
	}
	return payload
}

func envelopeFromMap(msg map[string]interface{}) protocolv1.Envelope {
	envelope := protocolv1.Envelope{}
	if msgType, ok := msg["type"].(string); ok {
		envelope.Type = msgType
	}
	if payload, ok := msg["payload"]; ok {
		if data, err := json.Marshal(payload); err == nil {
			envelope.Payload = data
		}
	}
	return envelope
}
