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
	"strings"
)

const maxLogGroups = 1024

// logGroup refers exclusively to the authoritative step log buffer. A command
// header is the original echoed line, never a second copy of its text.
type logGroup struct {
	start     int
	headerEnd int
	command   bool
	collapsed bool
	truncated bool
	gap       bool
}

type logGroups struct {
	commands    map[string]struct{}
	groups      []logGroup
	parsed      int // start of the incomplete line
	skipPartial bool
	collapsed   bool
}

func eligibleCommands(commands []string) []string {
	var eligible []string
	for _, command := range commands {
		if command != "" && !strings.ContainsAny(command, "\r\n") {
			eligible = append(eligible, strings.Clone(command))
		}
	}
	return eligible
}

func newLogGroups(commands []string) logGroups {
	g := logGroups{commands: make(map[string]struct{})}
	for _, command := range eligibleCommands(commands) {
		g.commands[command] = struct{}{}
	}
	return g
}

// update consumes newly completed lines after the caller appends and bounds its
// byte buffer. The evicted argument counts bytes removed from its front.
// Call on every append, even when no commands are configured. If eviction cuts
// an unparsed line, conservatively ignore its remainder rather than recognizing
// a suffix as a command echo.
func (g *logGroups) update(buffer []byte, evicted int) {
	if len(g.groups) == 0 {
		g.groups = append(g.groups, logGroup{})
	}
	if evicted > 0 {
		first := 0
		for first+1 < len(g.groups) && g.groups[first+1].start <= evicted {
			first++
		}
		g.groups = append(g.groups[:0], g.groups[first:]...)
		for i := range g.groups {
			group := &g.groups[i]
			group.start -= evicted
			group.headerEnd -= evicted
			if group.start < 0 {
				group.start = 0
				group.command = false // its echo is no longer intact
				group.headerEnd = 0
			}
		}
		g.groups[0].truncated = true
		if evicted > g.parsed {
			g.skipPartial = true
		}
		g.parsed = max(0, g.parsed-evicted)
	}
	for g.parsed < len(buffer) {
		end := bytes.IndexByte(buffer[g.parsed:], '\n')
		if end < 0 {
			break
		}
		end += g.parsed + 1
		line := bytes.TrimRight(buffer[g.parsed:end], "\r\n")
		if !g.skipPartial && bytes.HasPrefix(line, []byte("+ ")) {
			if _, ok := g.commands[string(line[2:])]; ok {
				if len(g.groups) < maxLogGroups {
					g.groups = append(g.groups, logGroup{start: g.parsed, headerEnd: end, command: true, collapsed: g.collapsed})
				} else {
					// Overflow commands must not disappear into a collapsed body.
					// Leave the remaining output ungrouped until eviction frees space.
					g.groups[len(g.groups)-1].command = false
				}
			}
		}
		g.skipPartial = false
		g.parsed = end
	}
}

// makeRoomForGap forgets a command boundary rather than a discontinuity. If
// only gaps remain, the caller must evict the returned prefix from its buffer
// and pass that byte count to update before calling gap.
func (g *logGroups) makeRoomForGap() int {
	if len(g.groups) < maxLogGroups {
		return 0
	}
	for i := 1; i < len(g.groups); i++ {
		if !g.groups[i].gap {
			g.groups[i-1].command = false
			copy(g.groups[i:], g.groups[i+1:])
			g.groups = g.groups[:len(g.groups)-1]
			return 0
		}
	}
	if prefix := g.groups[1].start; prefix > 0 {
		return prefix
	}
	// An empty first segment needs no byte eviction, but still occupies a slot.
	copy(g.groups, g.groups[1:])
	g.groups = g.groups[:len(g.groups)-1]
	return 0
}

// gap prevents a missing ingress prefix from completing or inventing an echo.
// Output following the gap stays visible until a subsequent complete match.
// The caller must first makeRoomForGap and apply any required prefix eviction.
func (g *logGroups) gap(offset int) {
	g.groups = append(g.groups, logGroup{start: offset, gap: true})
	g.parsed = offset
	g.skipPartial = true
}

// toggleCollapsed affects recognized commands only; initialization and a group
// whose header was evicted always remain visible.
func (g *logGroups) toggleCollapsed() {
	g.collapsed = !g.collapsed
	for i := range g.groups {
		g.groups[i].collapsed = g.collapsed
	}
}

// render leaves expanded bytes unchanged. Collapsing keeps the exact command
// echo and replaces its body with a visible marker. It never hides initialization
// output, including retained output whose command header has been evicted.
func (g *logGroups) render(buffer []byte) string {
	if len(g.groups) == 0 {
		return string(buffer)
	}
	var out strings.Builder
	for i, group := range g.groups {
		if group.gap {
			out.WriteString("\n[log bytes discarded]\n")
		}
		end := len(buffer)
		if i+1 < len(g.groups) {
			end = g.groups[i+1].start
		}
		if group.command && group.collapsed {
			out.Write(buffer[group.start:group.headerEnd])
			if group.headerEnd < end {
				out.WriteString("  [output collapsed]\n")
			}
		} else {
			out.Write(buffer[group.start:end])
		}
	}
	return out.String()
}
