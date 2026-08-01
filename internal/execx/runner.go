package execx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Request struct {
	Command   string
	Args      []string
	Dir       string
	Env       []string
	Stdin     io.Reader
	Sensitive bool
	CleanEnv  bool
}

type Result struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	ExitCode int    `json:"exit_code"`
}

type Runner interface {
	Run(context.Context, Request) (Result, error)
	LookPath(string) (string, error)
}

type OSRunner struct{}

func (OSRunner) LookPath(name string) (string, error) { return exec.LookPath(name) }

func (OSRunner) Run(ctx context.Context, req Request) (Result, error) {
	if strings.TrimSpace(req.Command) == "" {
		return Result{}, fmt.Errorf("command is required")
	}
	cmd := exec.CommandContext(ctx, req.Command, req.Args...)
	cmd.Dir = req.Dir
	if req.CleanEnv {
		cmd.Env = append(minimalEnvironment(), req.Env...)
	} else {
		cmd.Env = append(os.Environ(), req.Env...)
	}
	cmd.Stdin = req.Stdin
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	result := Result{Stdout: stdout.String(), Stderr: stderr.String()}
	if err == nil {
		return result, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		result.ExitCode = exitErr.ExitCode()
		if req.Sensitive {
			return result, fmt.Errorf("%s exited with code %d (sensitive output redacted)", req.Command, result.ExitCode)
		}
		return result, fmt.Errorf("%s exited with code %d: %s", req.Command, result.ExitCode, strings.TrimSpace(result.Stderr))
	}
	return result, fmt.Errorf("run %s: %w", req.Command, err)
}

func minimalEnvironment() []string {
	keys := []string{"PATH", "Path", "PATHEXT", "SystemRoot", "COMSPEC", "TEMP", "TMP", "TMPDIR", "LANG", "LC_ALL"}
	env := make([]string, 0, len(keys))
	seen := map[string]bool{}
	for _, key := range keys {
		if value, ok := os.LookupEnv(key); ok && !seen[strings.ToUpper(key)] {
			env = append(env, key+"="+value)
			seen[strings.ToUpper(key)] = true
		}
	}
	return env
}
