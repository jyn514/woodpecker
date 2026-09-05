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
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLogGroupsExactMatchingGolden(t *testing.T) {
	g := newLogGroups([]string{"echo hello", "printf 'hi'", "", "multi\nline", "bad\rcommand"})
	buffer := []byte("initializing\n+  echo hello\n+ echo \"hello\"\n+ multi\nline\n+ echo hello\r\nhello\n+ printf 'hi'\nhi\n")
	g.update(buffer, 0)
	require.Equal(t, string(buffer), g.render(buffer))
	g.toggleCollapsed()
	require.Equal(t, "initializing\n+  echo hello\n+ echo \"hello\"\n+ multi\nline\n+ echo hello\r\n  [output collapsed]\n+ printf 'hi'\n  [output collapsed]\n", g.render(buffer))
	g.toggleCollapsed()
	require.Equal(t, string(buffer), g.render(buffer))
	require.Equal(t, []string{"echo hello", "printf 'hi'"}, eligibleCommands([]string{"echo hello", "printf 'hi'", "", "multi\nline", "bad\rcommand"}))
}

func TestLogGroupsFragmentsAndExactBytes(t *testing.T) {
	for _, echo := range []string{"+ echo hello\n", "+ echo hello\r\n"} {
		for split := 0; split <= len(echo); split++ {
			g := newLogGroups([]string{"echo hello"})
			g.update([]byte(echo[:split]), 0)
			g.update([]byte(echo), 0)
			buffer := []byte(echo + "result")
			g.update(buffer, 0)
			g.toggleCollapsed()
			require.Equal(t, echo+"  [output collapsed]\n", g.render(buffer))
		}
	}
	for _, echo := range []string{"+ echo hello ", "+  echo hello", "+ echo 'hello'", "\x1b[32m+ echo hello", "+ echo hello\rnot"} {
		g := newLogGroups([]string{"echo hello"})
		buffer := []byte(echo + "\nresult\n")
		g.update(buffer, 0)
		g.toggleCollapsed()
		require.Equal(t, string(buffer), g.render(buffer))
	}
}

func TestLogGroupsEvictionPreservesVisibleOutput(t *testing.T) {
	g := newLogGroups([]string{"one", "two"})
	buffer := []byte("setup\n+ one\nfirst\n+ two\nsecond\n")
	g.update(buffer, 0)
	g.toggleCollapsed()
	evicted := len("setup\n+ one\n")
	buffer = buffer[evicted:]
	g.update(buffer, evicted)
	require.True(t, g.groups[0].truncated)
	require.Equal(t, "first\n+ two\n  [output collapsed]\n", g.render(buffer))
	evicted = len("first\n")
	buffer = buffer[evicted:]
	g.update(buffer, evicted)
	require.Equal(t, "+ two\n  [output collapsed]\n", g.render(buffer))
	require.True(t, g.groups[0].truncated)
}

func TestLogGroupsOversizedPartialIsNotAnEcho(t *testing.T) {
	g := newLogGroups([]string{"one"})
	// A suffix exposed by fragment eviction must not become a new command.
	prefix := strings.Repeat("x", 1<<20)
	g.update([]byte(prefix+"+ one"), 0)
	buffer := []byte("+ one\nordinary\n+ one\nreal output\n")
	g.update(buffer, len(prefix))
	g.toggleCollapsed()
	require.Equal(t, "+ one\nordinary\n+ one\n  [output collapsed]\n", g.render(buffer))
}

func TestLogGroupsMetadataBoundAndEvictionRecovery(t *testing.T) {
	g := newLogGroups([]string{"one"})
	buffer := []byte(strings.Repeat("+ one\nresult\n", 2000))
	g.update(buffer, 0)
	require.LessOrEqual(t, len(g.groups), 1024)
	require.Equal(t, string(buffer), g.render(buffer))
	evicted := len(buffer)
	buffer = []byte("+ one\nfresh\n")
	g.update(buffer, evicted)
	require.LessOrEqual(t, len(g.groups), 2)
	g.toggleCollapsed()
	require.Equal(t, "+ one\n  [output collapsed]\n", g.render(buffer))
}

func TestLogGroupsZeroValue(t *testing.T) {
	var g logGroups
	buffer := []byte("+ anything\noutput\n")
	g.update(buffer, 0)
	g.toggleCollapsed()
	require.Equal(t, string(buffer), g.render(buffer))
}

func TestLogGroupsOverflowRemainsVisibleWhenCollapsed(t *testing.T) {
	for _, collapseBeforeOutput := range []bool{false, true} {
		name := "collapse after output"
		if collapseBeforeOutput {
			name = "collapse before output"
		}
		t.Run(name, func(t *testing.T) {
			g := newLogGroups([]string{"one", "last"})
			if collapseBeforeOutput {
				g.toggleCollapsed()
			}
			// A verbose shell loop exhausts grouping metadata, then another
			// configured command runs. Both its echo and result must stay visible.
			buffer := []byte(strings.Repeat("+ one\nloop output\n", 2000))
			g.update(buffer, 0)
			buffer = append(buffer, []byte("+ last\nfinal result\n")...)
			g.update(buffer, 0)
			if !collapseBeforeOutput {
				g.toggleCollapsed()
			}
			rendered := g.render(buffer)
			require.LessOrEqual(t, len(g.groups), 1024)
			require.Equal(t, 2000, strings.Count(rendered, "+ one\n"))
			require.True(t, strings.HasSuffix(rendered, "+ one\nloop output\n+ last\nfinal result\n"))
			require.Contains(t, rendered, "[output collapsed]")
			g.toggleCollapsed()
			require.Equal(t, string(buffer), g.render(buffer))
		})
	}
}

func TestLogGroupsGapCapacityPreservesDiscontinuities(t *testing.T) {
	g := newLogGroups([]string{"one"})
	buffer := []byte("result: SU")
	g.update(buffer, 0)
	g.gap(len(buffer))
	buffer = append(buffer, []byte("CCESS\n"+strings.Repeat("+ one\nbody\n", 1022))...)
	g.update(buffer, 0)
	g.toggleCollapsed()
	require.Len(t, g.groups, 1024)
	require.Zero(t, g.makeRoomForGap())
	g.gap(len(buffer))
	buffer = append(buffer, []byte("after second gap\n")...)
	g.update(buffer, 0)
	rendered := g.render(buffer)
	require.Len(t, g.groups, 1024)
	require.NotContains(t, rendered, "result: SUCCESS")
	require.Contains(t, rendered, "result: SU\n[log bytes discarded]\nCCESS\n")
	require.Equal(t, 2, strings.Count(rendered, "[log bytes discarded]"))
	require.Contains(t, rendered, "after second gap\n")
	// The forgotten command's header and body remain visible, not folded
	// into a preceding collapsed group.
	require.Contains(t, rendered, "+ one\nbody\n")
}

func TestLogGroupsGapCapacityEvictsPrefixWhenOnlyGapsRemain(t *testing.T) {
	for _, prefix := range []string{"old prefix", ""} {
		name := "nonempty first segment"
		if prefix == "" {
			name = "empty first segment"
		}
		t.Run(name, func(t *testing.T) {
			g := newLogGroups(nil)
			buffer := []byte(prefix)
			g.update(buffer, 0)
			for range 1023 {
				g.gap(len(buffer))
				buffer = append(buffer, []byte("fragment")...)
				g.update(buffer, 0)
			}
			require.Len(t, g.groups, 1024)
			for iteration := range 2 {
				before := g.render(buffer)
				evicted := g.makeRoomForGap()
				if iteration == 0 {
					require.Equal(t, len(prefix), evicted)
					before = strings.TrimPrefix(before, prefix)
				} else {
					require.Equal(t, len("fragment"), evicted)
					before = strings.TrimPrefix(before, "\n[log bytes discarded]\nfragment")
				}
				buffer = buffer[evicted:]
				g.update(buffer, evicted)
				require.Less(t, len(g.groups), 1024)
				require.Equal(t, before, g.render(buffer))
				g.gap(len(buffer))
				buffer = append(buffer, []byte("new fragment")...)
				g.update(buffer, 0)
				require.Len(t, g.groups, 1024)
				require.Equal(t, before+"\n[log bytes discarded]\nnew fragment", g.render(buffer))
			}
		})
	}
}
