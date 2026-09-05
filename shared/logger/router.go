// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package logger

import (
	"io"
	"sync"

	"go.uber.org/multierr"
)

type captureTarget struct {
	writer       io.Writer
	suppressBase bool
}

type outputRouter struct {
	mu       sync.RWMutex
	base     io.Writer
	nextID   uint64
	captures map[uint64]captureTarget
}

func (r *outputRouter) Write(p []byte) (int, error) {
	r.mu.RLock()
	base := r.base
	captures := make([]captureTarget, 0, len(r.captures))
	suppressBase := false
	for _, target := range r.captures {
		captures = append(captures, target)
		suppressBase = suppressBase || target.suppressBase
	}
	r.mu.RUnlock()

	var err error
	for _, target := range captures {
		_, captureErr := target.writer.Write(p)
		err = multierr.Append(err, captureErr)
	}
	if base != nil && !suppressBase {
		_, baseErr := base.Write(p)
		err = multierr.Append(err, baseErr)
	}
	return len(p), err
}

func (r *outputRouter) setBase(base io.Writer) {
	r.mu.Lock()
	r.base = base
	r.mu.Unlock()
}

func (r *outputRouter) capture(writer io.Writer, suppressBase bool) func() {
	r.mu.Lock()
	if r.captures == nil {
		r.captures = make(map[uint64]captureTarget)
	}
	r.nextID++
	id := r.nextID
	r.captures[id] = captureTarget{writer: writer, suppressBase: suppressBase}
	r.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			delete(r.captures, id)
			r.mu.Unlock()
		})
	}
}

var globalOutput = new(outputRouter)

// CaptureGlobalOutput routes global logger output to capture until the returned
// restore function is called. When suppressBase is true, output is withheld
// from the configured destination while this capture is active.
func CaptureGlobalOutput(capture io.Writer, suppressBase bool) func() {
	return globalOutput.capture(capture, suppressBase)
}
