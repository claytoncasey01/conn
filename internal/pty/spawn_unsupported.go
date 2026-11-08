//go:build !darwin && !linux

package pty

// Spawn is unsupported on this system: Conn hosts programs on macOS and Linux only.
func Spawn(_ Spec) (Process, error) { return nil, ErrUnsupported }
