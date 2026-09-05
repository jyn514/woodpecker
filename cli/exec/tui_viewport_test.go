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
	"testing"
	"time"

	"charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestTerminalNavigationKeysControlActiveLog(t *testing.T) {
	m := extensionModel()
	m.workflow.Stages[0].Steps[0].Commands = []string{"echo run"}
	m.appendLog("a", []byte("+ echo run\n0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n"), false)
	log := m.activeLog()
	visible := func() []string { return log.viewport.visible(log.lines(), m.logHeight()) }
	require.Equal(t, []string{"7", "8", "9"}, visible())
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgUp}))
	require.Equal(t, []string{"4", "5", "6"}, visible())
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyPgDown}))
	require.Equal(t, []string{"7", "8", "9"}, visible())
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyHome}))
	require.Equal(t, []string{"+ echo run", "0", "1"}, visible())
	key(m, "f")
	require.Equal(t, []string{"7", "8", "9"}, visible())
	key(m, "c")
	require.Equal(t, []string{"+ echo run", "  [output collapsed]"}, visible())
	key(m, "c")
	m.Update(tea.KeyPressMsg(tea.Key{Code: tea.KeyEnd}))
	require.Equal(t, []string{"7", "8", "9"}, visible())
}

func TestLogViewPreservesCompletedBlankLines(t *testing.T) {
	m := extensionModel()
	m.appendLog("a", []byte("alpha\n\n"), false)
	require.Contains(t, m.View().Content, "Logs\nalpha\n\n↑")
	m.showDiagnostics = true
	m.appendDiagnostic([]byte("notice\n\n"))
	require.Contains(t, m.View().Content, "Diagnostics\nnotice\n\nd steps")
}

// Completing a stream must expose its final partial line without moving a
// reader who paused on older retained output.
func TestCompletionKeepsPausedViewportAnchored(t *testing.T) {
	for _, diagnostics := range []bool{false, true} {
		name := "step"
		if diagnostics {
			name = "diagnostics"
		}
		t.Run(name, func(t *testing.T) {
			m := extensionModel()
			m.showDiagnostics = diagnostics
			appendData := func(data string) {
				if diagnostics {
					m.appendDiagnostic([]byte(data))
				} else {
					m.appendLog("a", []byte(data), false)
				}
			}
			appendData("0\n1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n")
			key(m, "home")
			log := m.activeLog()
			before := log.viewport.visible(log.lines(), m.logHeight())
			appendData("partial")
			require.Equal(t, before, log.viewport.visible(log.lines(), m.logHeight()))
			if diagnostics {
				m.Update(commandFinishedMsg{})
			} else {
				m.Update(workflowFinishedMsg{id: "w", At: time.Now()})
			}
			require.Equal(t, before, log.viewport.visible(log.lines(), m.logHeight()))
			key(m, "end")
			require.Contains(t, log.viewport.visible(log.lines(), m.logHeight()), "partial")
		})
	}
}
