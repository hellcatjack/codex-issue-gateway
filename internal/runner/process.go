package runner

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
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
	env := os.Environ()
	for k, v := range extra {
		prefix := k + "="
		replaced := false
		for i := range env {
			if strings.HasPrefix(env[i], prefix) {
				env[i] = prefix + v
				replaced = true
				break
			}
		}
		if !replaced {
			env = append(env, prefix+v)
		}
	}
	return env
}
