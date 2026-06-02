package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"time"
)

type CommandSpec struct {
	Name  string
	Args  []string
	Dir   string
	Env   map[string]string
	Stdin string
}

type Activity struct {
	Time  time.Time
	Kind  string
	Bytes int
}

type ProcessInput struct {
	Name       string
	Args       []string
	Dir        string
	Env        map[string]string
	Stdin      string
	OnActivity func(Activity)
}

type ProcessResult struct {
	ExitCode int
	Stdout   string
	Stderr   string
}

type TestCommandsResult struct {
	Passed  bool
	Results []ProcessResult
}

func RunProcess(ctx context.Context, input ProcessInput) (ProcessResult, error) {
	cmd := exec.CommandContext(ctx, input.Name, input.Args...)
	cmd.Dir = input.Dir
	cmd.Env = mergeEnv(input.Env)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return ProcessResult{}, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return ProcessResult{}, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return ProcessResult{}, err
	}
	if err := cmd.Start(); err != nil {
		return ProcessResult{}, err
	}
	_, writeErr := io.WriteString(stdin, input.Stdin)
	closeErr := stdin.Close()

	var outBuf, errBuf bytes.Buffer
	done := make(chan error, 2)
	go copyActivity(&outBuf, stdout, "stdout", input.OnActivity, done)
	go copyActivity(&errBuf, stderr, "stderr", input.OnActivity, done)
	copyErr1 := <-done
	copyErr2 := <-done
	waitErr := cmd.Wait()

	result := ProcessResult{Stdout: outBuf.String(), Stderr: errBuf.String(), ExitCode: cmd.ProcessState.ExitCode()}
	if writeErr != nil {
		return result, writeErr
	}
	if closeErr != nil {
		return result, closeErr
	}
	if copyErr1 != nil {
		return result, copyErr1
	}
	if copyErr2 != nil {
		return result, copyErr2
	}
	if waitErr != nil {
		return result, fmt.Errorf("process failed: %w", waitErr)
	}
	return result, nil
}

func RunTestCommands(ctx context.Context, repoDir string, commands []string, onActivity func(Activity)) (TestCommandsResult, error) {
	return RunTestCommandsWithEnv(ctx, repoDir, commands, nil, onActivity)
}

func RunTestCommandsWithEnv(ctx context.Context, repoDir string, commands []string, env map[string]string, onActivity func(Activity)) (TestCommandsResult, error) {
	out := TestCommandsResult{Passed: true}
	for _, command := range commands {
		result, err := RunProcess(ctx, ProcessInput{
			Name:       "sh",
			Args:       []string{"-c", command},
			Dir:        repoDir,
			Env:        env,
			OnActivity: onActivity,
		})
		out.Results = append(out.Results, result)
		if err != nil || result.ExitCode != 0 {
			out.Passed = false
			return out, err
		}
	}
	return out, nil
}

func copyActivity(dst *bytes.Buffer, src io.Reader, kind string, onActivity func(Activity), done chan<- error) {
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			_, _ = dst.Write(buf[:n])
			if onActivity != nil {
				onActivity(Activity{Time: time.Now(), Kind: kind, Bytes: n})
			}
		}
		if err == io.EOF {
			done <- nil
			return
		}
		if err != nil {
			done <- err
			return
		}
	}
}

func mergeEnv(extra map[string]string) []string {
	values := map[string]string{}
	if path := os.Getenv("PATH"); path != "" {
		values["PATH"] = path
	}
	values["HOME"] = os.TempDir()
	values["TMPDIR"] = os.TempDir()
	for k, v := range extra {
		values[k] = v
		if k == "CODEX_HOME" {
			values["HOME"] = v
		}
	}
	env := make([]string, 0, len(values))
	for k, v := range values {
		env = append(env, k+"="+v)
	}
	return env
}
