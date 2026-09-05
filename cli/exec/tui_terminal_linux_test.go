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
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// Exercise the real terminal reader, not Program.Send: losing raw-mode Ctrl-C,
// returning before backend cleanup, or skipping terminal restoration must fail.
func TestTUITerminalCleanup(t *testing.T) {
	for _, key := range []string{"q", "ctrl-c"} {
		t.Run(key, func(t *testing.T) {
			master, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
			require.NoError(t, err)
			defer master.Close()
			require.NoError(t, unix.IoctlSetPointerInt(int(master.Fd()), unix.TIOCSPTLCK, 0))
			number, err := unix.IoctlGetInt(int(master.Fd()), unix.TIOCGPTN)
			require.NoError(t, err)
			slave, err := os.OpenFile(fmt.Sprintf("/dev/pts/%d", number), os.O_RDWR|unix.O_NOCTTY, 0)
			require.NoError(t, err)
			defer slave.Close()
			require.NoError(t, unix.IoctlSetWinsize(int(slave.Fd()), unix.TIOCSWINSZ, &unix.Winsize{Row: 24, Col: 100}))
			before, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
			require.NoError(t, err)
			statusR, statusW, err := os.Pipe()
			require.NoError(t, err)
			defer statusR.Close()
			defer statusW.Close()
			releaseR, releaseW, err := os.Pipe()
			require.NoError(t, err)
			defer releaseR.Close()
			defer releaseW.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestTUITerminalHelper$", "-test.count=1")
			cmd.Env = append(os.Environ(), "WOODPECKER_TUI_PTY_HELPER=1", "TERM=xterm-256color")
			cmd.Stdin, cmd.Stdout, cmd.Stderr = slave, slave, slave
			cmd.ExtraFiles = []*os.File{statusW, releaseR}
			cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true, Setctty: true, Ctty: 0}
			var mu sync.Mutex
			var output strings.Builder
			go func() {
				buffer := make([]byte, 4096)
				for {
					n, readErr := master.Read(buffer)
					mu.Lock()
					output.Write(buffer[:n])
					mu.Unlock()
					if readErr != nil {
						return
					}
				}
			}()
			transcript := func() string { mu.Lock(); defer mu.Unlock(); return output.String() }
			require.NoError(t, cmd.Start())
			done := make(chan error, 1)
			go func() { done <- cmd.Wait() }()
			defer func() { cancel(); <-ctx.Done() }()
			statusW.Close()
			releaseR.Close()
			statuses := make(chan string, 4)
			go func() {
				scanner := bufio.NewScanner(statusR)
				for scanner.Scan() {
					statuses <- scanner.Text()
				}
				close(statuses)
			}()
			require.Eventually(t, func() bool { return strings.Contains(transcript(), "q quit") }, 5*time.Second, 10*time.Millisecond)
			raw, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
			require.NoError(t, err)
			require.Zero(t, raw.Lflag&(unix.ICANON|unix.ECHO|unix.ISIG))
			if key == "q" {
				_, err = master.Write([]byte("q"))
				require.NoError(t, err)
				require.Eventually(t, func() bool { return strings.Contains(transcript(), "Cancel this command") }, time.Second, 10*time.Millisecond)
				select {
				case status := <-statuses:
					t.Fatalf("q canceled without confirmation: %q", status)
				default:
				}
				_, err = master.Write([]byte("y"))
			} else {
				_, err = master.Write([]byte{3})
			}
			require.NoError(t, err)
			select {
			case status := <-statuses:
				require.Equal(t, "cleaning", status)
			case <-ctx.Done():
				t.Fatalf("key did not cancel execution: %s", transcript())
			}
			// Cleanup is held at a deterministic barrier; Run must not return yet.
			select {
			case status := <-statuses:
				t.Fatalf("returned before cleanup: %q", status)
			case err := <-done:
				t.Fatalf("process exited before cleanup: %v", err)
			case <-time.After(100 * time.Millisecond):
			}
			duringCleanup, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
			require.NoError(t, err)
			require.Equal(t, raw, duringCleanup, "terminal restored before backend cleanup completed")
			require.NotContains(t, transcript(), "\x1b[?1049l", "alternate screen left before cleanup")
			_, err = releaseW.Write([]byte{1})
			require.NoError(t, err)
			select {
			case err := <-done:
				require.NoError(t, err, transcript())
			case <-ctx.Done():
				t.Fatalf("session did not finish: %s", transcript())
			}
			require.Equal(t, "returned", <-statuses)
			after, err := unix.IoctlGetTermios(int(slave.Fd()), unix.TCGETS)
			require.NoError(t, err)
			require.Equal(t, before, after, "terminal settings must survive a canceled session")
			require.Eventually(t, func() bool { return strings.Contains(transcript(), "\x1b[?1049l") }, time.Second, time.Millisecond, "alternate screen was not restored")
		})
	}
}

func TestTUITerminalHelper(t *testing.T) {
	if os.Getenv("WOODPECKER_TUI_PTY_HELPER") != "1" {
		return
	}
	status := os.NewFile(3, "status")
	release := os.NewFile(4, "release")
	defer status.Close()
	defer release.Close()
	session := newTUISession(os.Stdin, os.Stdout)
	cleaned := false
	err := session.Run(context.Background(), func(ctx context.Context) error {
		session.BeginWorkflow(execWorkflow{ID: "pty-workflow"})
		<-ctx.Done()
		fmt.Fprintln(status, "cleaning")
		var signal [1]byte
		if _, err := io.ReadFull(release, signal[:]); err != nil {
			return err
		}
		cleaned = true
		return ctx.Err()
	})
	if !cleaned || !errors.Is(err, context.Canceled) {
		t.Fatalf("Run returned before cleanup or lost cancellation: cleaned=%v err=%v", cleaned, err)
	}
	fmt.Fprintln(status, "returned")
}
