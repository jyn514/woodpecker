// Copyright 2026 Woodpecker Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package exec

import (
	"bytes"
	"fmt"
	"testing"
)

// Seed each history through appendLog, independently so fixture construction
// does not itself pay the cross-log cost this workload is meant to detect.
func logCostModel(logCount int, paused bool) *tuiModel {
	m := newTUIModel(func() {})
	m.width, m.height = 80, 24
	steps := make([]execStep, logCount)
	line := append(bytes.Repeat([]byte{'x'}, 127), '\n')
	history := bytes.Repeat(line, ((1<<20)-4096)/len(line))
	for i := range steps {
		id := fmt.Sprintf("step-%d", i)
		steps[i] = execStep{ID: id, Name: id}
		seed := newTUIModel(func() {})
		seed.appendLog(id, history, false)
		m.logs[id] = seed.logs[id]
		if paused && i != 0 {
			m.logs[id].viewport.move("pgup", len(m.logs[id].lines()), 10)
		}
	}
	m.workflow = execWorkflow{ID: "w", Name: "build", Stages: []execStage{{Steps: steps}}}
	return m
}

// The payload is an ordinary complete line: it cannot create or mutate command
// groups. Restore the target's byte length, parser cursor and viewport after each
// append, so calibration never turns this into a retention/eviction benchmark.
// Reserve payload capacity before measurement, including for diagnostics.
func logCostAppend(m *tuiModel, diagnostic bool) func() {
	fragment := append(bytes.Repeat([]byte{'y'}, 31), '\n')
	target := m.logs["step-0"]
	if diagnostic {
		target = &m.diagnostics
	}
	data := make([]byte, len(target.data), len(target.data)+len(fragment))
	copy(data, target.data)
	target.data = data
	before := *target
	return func() {
		if diagnostic {
			m.appendDiagnostic(fragment)
		} else {
			m.appendLog("step-0", fragment, false)
		}
		*target = before
	}
}

func BenchmarkTUIAppendRetainedLogs(b *testing.B) {
	for _, paused := range []bool{false, true} {
		for _, diagnostic := range []bool{false, true} {
			kind := "step"
			if diagnostic {
				kind = "diagnostic"
			}
			for _, count := range []int{1, 20, 100} {
				b.Run(fmt.Sprintf("%s/paused=%t/logs=%d", kind, paused, count), func(b *testing.B) {
					appendFragment := logCostAppend(logCostModel(count, paused), diagnostic)
					b.ReportAllocs()
					b.SetBytes(32)
					b.ResetTimer()
					for range b.N {
						appendFragment()
					}
				})
			}
		}
	}
}

// Retaining inactive steps must not multiply the allocation cost of each
// incoming fragment. This catches append-time rendering/splitting of every
// history without asserting a particular implementation's allocation count or
// relying on noisy wall-clock thresholds.
func TestTUIAppendCostIndependentOfUnrelatedLogs(t *testing.T) {
	for _, paused := range []bool{false, true} {
		for _, diagnostic := range []bool{false, true} {
			kind := "step"
			if diagnostic {
				kind = "diagnostic"
			}
			t.Run(fmt.Sprintf("%s/paused=%t", kind, paused), func(t *testing.T) {
				allocs := func(count int) float64 {
					appendFragment := logCostAppend(logCostModel(count, paused), diagnostic)
					return testing.AllocsPerRun(16, appendFragment)
				}
				one, many := allocs(1), allocs(20)
				t.Logf("allocations per append: 1 retained log = %.1f; 20 retained logs = %.1f", one, many)
				// Allow incidental bookkeeping, but not even one allocation per
				// unrelated history. The append target and payload are identical.
				if many > one+4 {
					t.Errorf("unrelated retained logs increase append allocations: %.1f -> %.1f (allowed increase: 4)", one, many)
				}
			})
		}
	}
}
