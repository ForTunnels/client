// SPDX-License-Identifier: PROPRIETARY
// Copyright (c) 2026 ForTunnels

package control

import "fmt"

type Output interface {
	Printf(format string, args ...any)
	Println(args ...any)
}

type StdOutput struct{}

func (StdOutput) Printf(format string, args ...any) { fmt.Printf(format, args...) }
func (StdOutput) Println(args ...any)               { fmt.Println(args...) }
