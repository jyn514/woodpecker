// Copyright 2022 Woodpecker Authors
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
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"codeberg.org/6543/xyaml/v2"
	"github.com/oklog/ulid/v2"
	"github.com/rs/zerolog/log"
	"github.com/urfave/cli/v3"
	"go.uber.org/multierr"
	"golang.org/x/term"

	"go.woodpecker-ci.org/woodpecker/v3/cli/lint"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/docker"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/kubernetes"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/local"
	backend_types "go.woodpecker-ci.org/woodpecker/v3/pipeline/backend/types"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/frontend/builder"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/frontend/metadata"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/frontend/yaml/compiler"
	"go.woodpecker-ci.org/woodpecker/v3/pipeline/logging"
	pipeline_runtime "go.woodpecker-ci.org/woodpecker/v3/pipeline/runtime"
	pipeline_utils "go.woodpecker-ci.org/woodpecker/v3/pipeline/utils"
	"go.woodpecker-ci.org/woodpecker/v3/shared/constant"
	"go.woodpecker-ci.org/woodpecker/v3/shared/utils"
)

// Command exports the exec command.
var Command = &cli.Command{
	Name:      "exec",
	Usage:     "execute a local pipeline",
	ArgsUsage: "[path/to/.woodpecker.yaml]",
	Action:    run,
	Flags:     slices.Concat(flags, docker.Flags, kubernetes.Flags, local.Flags),
}

var backends = []backend_types.Backend{
	kubernetes.New(),
	docker.New(),
	local.New(),
}

func run(ctx context.Context, c *cli.Command) error {
	useTUI, err := selectTUI(c.Bool("tui"), c.Bool("no-tui"), term.IsTerminal(int(os.Stdin.Fd())), term.IsTerminal(int(os.Stdout.Fd())))
	if err != nil {
		return err
	}

	var session execSession = newPlainSession(sessionWriters{stdout: os.Stdout, stderr: os.Stderr})
	if useTUI {
		if err := validateTUITerminals(os.Stdin, os.Stdout); err != nil {
			return err
		}
		logDestination := c.String("log-file")
		suppressTerminalDiagnostics := logDestination == "" || logDestination == "stdout" || logDestination == "stderr"
		session = newTUISession(os.Stdin, os.Stdout, suppressTerminalDiagnostics)
	}
	defer session.Close()
	return session.Run(ctx, func(ctx context.Context) error { return dispatch(ctx, c, session) })
}

func selectTUI(force, disable, inputTerminal, outputTerminal bool) (bool, error) {
	if force && disable {
		return false, fmt.Errorf("--tui and --no-tui cannot be used together")
	}
	if disable {
		return false, nil
	}
	if force {
		if !inputTerminal {
			return false, fmt.Errorf("--tui requires terminal input")
		}
		if !outputTerminal {
			return false, fmt.Errorf("--tui requires terminal output")
		}
		return true, nil
	}
	return inputTerminal && outputTerminal, nil
}

// TODO: do parallel runs with output to multiple _windows_ e.g. tmux like
func execDir(ctx context.Context, c *cli.Command, session execSession, dir string) error {
	// TODO: respect pipeline dependency
	repoPath := c.String("repo-path")
	if repoPath != "" {
		repoPath, _ = filepath.Abs(repoPath)
	} else {
		repoPath, _ = filepath.Abs(filepath.Dir(dir))
	}
	if runtime.GOOS == "windows" && c.String("backend-engine") != "local" {
		repoPath = convertPathForWindows(repoPath)
	}

	var yamls []*builder.YamlFile
	walkErr := filepath.Walk(dir, func(path string, info os.FileInfo, e error) error {
		if e != nil {
			return e
		}
		if info.Mode().IsRegular() && (strings.HasSuffix(info.Name(), ".yaml") || strings.HasSuffix(info.Name(), ".yml")) {
			dat, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			yamls = append(yamls, &builder.YamlFile{Name: path, Data: dat})
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	return runExec(ctx, c, session, yamls, repoPath)
}

func repoRootFromFile(file string) string {
	root := filepath.Dir(file)

	// first we check if file lives inside multi workflow setup
	if filepath.Base(root) == ".woodpecker" {
		log.Info().Msg("auto detected workflow in multi workflow setup, parent directory is used as workspace")
		root = filepath.Dir(root)
	}

	// and make sure have the absolute path
	root, _ = filepath.Abs(root)

	log.Debug().Msgf("auto selected workspace folder at %q", root)
	return root
}

func execFile(ctx context.Context, c *cli.Command, session execSession, file string) error {
	repoPath := c.String("repo-path")
	if repoPath != "" {
		repoPath, _ = filepath.Abs(repoPath)
	} else {
		repoPath = repoRootFromFile(file)
	}
	if runtime.GOOS == "windows" && c.String("backend-engine") != "local" {
		repoPath = convertPathForWindows(repoPath)
	}

	dat, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	return runExec(ctx, c, session, []*builder.YamlFile{{Name: file, Data: dat}}, repoPath)
}

func runExec(ctx context.Context, c *cli.Command, session execSession, yamls []*builder.YamlFile, repoPath string) error {
	// if we use the local backend we should signal to run at $repoPath
	if c.String("backend-engine") == "local" {
		local.CLIWorkaroundExecAtDir = repoPath
	}

	// collect secrets from flags
	var secrets []compiler.Secret
	for key, val := range c.StringMap("secrets") {
		secrets = append(secrets, compiler.Secret{Name: key, Value: val})
	}
	if secretsFile := c.String("secrets-file"); secretsFile != "" {
		fileContent, err := os.ReadFile(secretsFile)
		if err != nil {
			return err
		}
		var m map[string]string
		if err := xyaml.Unmarshal(fileContent, &m); err != nil {
			return err
		}
		for key, val := range m {
			secrets = append(secrets, compiler.Secret{Name: key, Value: val})
		}
	}

	// collect extra env vars from --env flags
	pipelineEnv := make(map[string]string)
	for _, env := range c.StringSlice("env") {
		before, after, _ := strings.Cut(env, "=")
		pipelineEnv[before] = after
	}

	privilegedPlugins := c.StringSlice("plugins-privileged")

	// prefix for the local-execution workspace volume name
	prefix := "wp_" + ulid.Make().String()

	// build compiler options — mirrors server behavior
	compilerOpts := []compiler.Option{
		compiler.WithEscalated(privilegedPlugins...),
		compiler.WithNetworks(c.StringSlice("network")...),
		compiler.WithProxy(compiler.ProxyOptions{
			NoProxy:    c.String("backend-no-proxy"),
			HTTPProxy:  c.String("backend-http-proxy"),
			HTTPSProxy: c.String("backend-https-proxy"),
		}),
		compiler.WithLocal(c.Bool("local")),
		compiler.WithNetrc(
			c.String("netrc-username"),
			c.String("netrc-password"),
			c.String("netrc-machine"),
		),
		compiler.WithSecret(secrets...),
		compiler.WithEnviron(pipelineEnv),
	}

	// configure volumes for local execution
	volumes := c.StringSlice("volumes")
	if c.Bool("local") {
		compilerOpts = append(
			compilerOpts,
			compiler.WithWorkspace(
				c.String("workspace-base"),
				c.String("workspace-path"),
			),
		)
		volumes = append(
			volumes,
			prefix+"_default:"+c.String("workspace-base"),
			repoPath+":"+c.String("workspace-base")+"/"+c.String("workspace-path"),
		)
	} else {
		compilerOpts = append(
			compilerOpts,
			compiler.WithWorkspace(
				c.String("workspace-base"),
				c.String("workspace-path"),
			),
		)
	}
	compilerOpts = append(compilerOpts, compiler.WithVolumes(volumes...))

	// build the metadata once — the CLI has a single pipeline context for all
	// workflows, so every workflow gets the same metadata.
	baseMetadata, err := metadataFromContext(ctx, c, nil)
	if err != nil {
		return fmt.Errorf("could not create metadata: %w", err)
	}

	b := builder.PipelineBuilder{
		Yamls: yamls,
		Envs:  pipelineEnv,
		RepoTrusted: &metadata.TrustedConfiguration{
			Network:  c.Bool("repo-trusted-network"),
			Volumes:  c.Bool("repo-trusted-volumes"),
			Security: c.Bool("repo-trusted-security"),
		},
		TrustedClonePlugins: constant.TrustedClonePlugins,
		PrivilegedPlugins:   privilegedPlugins,
		CompilerOptions:     compilerOpts,
		// GetWorkflowMetadata provides per-workflow metadata. In the CLI there
		// is no server context, so we derive it from the base metadata and
		// populate the workflow name/matrix from the builder.Workflow.
		GetWorkflowMetadata: func(w *builder.Workflow) metadata.Metadata {
			m := *baseMetadata
			m.Workflow = metadata.Workflow{
				Name:   w.Name,
				Number: w.PID,
				Matrix: w.Environ,
			}
			return m
		},
	}

	items, err := b.Build()
	if err != nil {
		str, fmtErr := lint.FormatLintError("pipeline", err, false)
		session.Print(outputStdout, str)
		if fmtErr != nil {
			return fmtErr
		}
	}

	if len(items) == 0 {
		return fmt.Errorf("no workflows to execute (all filtered out)")
	}

	backendCtx := context.WithValue(ctx, backend_types.CliCommand, c)
	backendEngine, err := backend.FindBackend(backendCtx, backends, c.String("backend-engine"))
	if err != nil {
		return err
	}
	if _, err = backendEngine.Load(backendCtx); err != nil {
		return err
	}

	var execErr error
	// TODO: respect depends_on and run in parallel where possible
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return multierr.Append(execErr, err)
		}
		session.Print(outputStdout, fmt.Sprintf("# %s\n", item.Workflow.Name))
		workflow := workflowDescriptor(item.Workflow.Name, item.Config)
		session.BeginWorkflow(workflow)

		pipelineCtx, cancel := context.WithTimeout(ctx, c.Duration("timeout"))
		pipelineCtx = utils.WithContextSigtermCallback(pipelineCtx, func() {
			session.Print(outputStdout, fmt.Sprintf("ctrl+c received, terminating workflow '%s'\n", item.Workflow.Name))
		})

		var workflowRuntime *pipeline_runtime.Runtime
		err := session.ExecuteWorkflow(pipelineCtx, workflow, func(workflowCtx context.Context) error {
			workflowRuntime = pipeline_runtime.New(
				item.Config, backendEngine,
				pipeline_runtime.WithContext(workflowCtx),
				pipeline_runtime.WithLogger(session.Logger(workflow.ID)),
				pipeline_runtime.WithTracer(session.Tracer(workflow.ID)),
				pipeline_runtime.WithDescription(map[string]string{
					"CLI": "exec",
				}),
			)
			return workflowRuntime.Run(ctx)
		})
		cancel()
		if err == nil {
			// Run does not report step failures as runtime errors, but a failed
			// step must still let the command exit non-zero.
			err = workflowRuntime.Err()
		}
		session.FinishWorkflow(workflow.ID, err)
		if err != nil {
			session.Print(outputStdout, fmt.Sprintln(err))
			execErr = multierr.Append(execErr, err)
		}
		session.Print(outputStdout, "\n")
	}
	return execErr
}

// convertPathForWindows converts a path to use slash separators
// for Windows. If the path is a Windows volume name like C:, it
// converts it to an absolute root path starting with slash (e.g.
// C: -> /c). Otherwise it just converts backslash separators to
// slashes.
func convertPathForWindows(path string) string {
	base := filepath.VolumeName(path)

	// Check if path is volume name like C:
	//nolint:mnd
	if len(base) == 2 {
		path = strings.TrimPrefix(path, base)
		base = strings.ToLower(strings.TrimSuffix(base, ":"))
		return "/" + base + filepath.ToSlash(path)
	}

	return filepath.ToSlash(path)
}

func lineLogger(output io.Writer) logging.Logger {
	return func(step *backend_types.Step, rc io.ReadCloser) error {
		logWriter := NewLineWriter(step.Name, step.UUID, output)
		return pipeline_utils.CopyLineByLine(logWriter, rc, pipeline.MaxLogLineLength)
	}
}
