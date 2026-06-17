// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package dataplane

import (
	"net"
	"sync"
)

// flowRegistry maps QUIC flow IDs to the local UDP peer address.
type flowRegistry struct {
	mu    sync.RWMutex
	flows map[string]*net.UDPAddr
}

func newFlowRegistry() *flowRegistry {
	return &flowRegistry{flows: make(map[string]*net.UDPAddr)}
}

func (r *flowRegistry) set(flowID string, addr *net.UDPAddr) {
	r.mu.Lock()
	r.flows[flowID] = addr
	r.mu.Unlock()
}

func (r *flowRegistry) get(flowID string) (*net.UDPAddr, bool) {
	r.mu.RLock()
	addr, ok := r.flows[flowID]
	r.mu.RUnlock()
	return addr, ok
}
