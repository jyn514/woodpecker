// Copyright 2026 Woodpecker Authors
// SPDX-License-Identifier: Apache-2.0

package exec

import (
	"os"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTUIRejectsNonTerminalBeforeRendering(t *testing.T) {
	input, err := os.Open(os.DevNull)
	require.NoError(t, err)
	defer input.Close()
	output, err := os.CreateTemp(t.TempDir(), "output")
	require.NoError(t, err)
	defer output.Close()
	assert.EqualError(t, validateTUITerminals(input, output), "--tui requires terminal input")
	data, err := os.ReadFile(output.Name())
	require.NoError(t, err)
	assert.Empty(t, data)
}

func TestTUIQuitConfirmationCancelsRunningWorkflow(t *testing.T) {
	canceled := false
	m := newTUIModel(func() { canceled = true })
	m.running = true
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'q', Text: "q"}))
	assert.True(t, m.confirming)
	assert.False(t, canceled)
	_, _ = m.Update(tea.KeyPressMsg(tea.Key{Code: 'y', Text: "y"}))
	assert.True(t, canceled)
}
