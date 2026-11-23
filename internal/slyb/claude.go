package slyb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"

	"github.com/nimling/nimbody/cli"
)

type ClaudeCall struct {
	Prompt string
	Schema string
	Model  string
	Tools  string
	Dir    string
}

type claudeReply struct {
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
}

func claudePath() (string, error) {
	settings, err := GetSettings()
	if err != nil {
		return "", err
	}
	if settings.Claude != "" {
		return settings.Claude, nil
	}
	path, err := exec.LookPath("claude")
	if err != nil {
		return "", &cli.Error{Code: cli.ExitFailure, Message: "claude is not on the PATH", Hint: "install Claude Code, then run `slyb install` so the browser finds it", Cause: err}
	}
	return path, nil
}

func PostClaude(ctx context.Context, call ClaudeCall, out any) error {
	path, err := claudePath()
	if err != nil {
		return err
	}
	args := []string{"-p", "--output-format", "json", "--json-schema", call.Schema, "--strict-mcp-config", "--no-session-persistence"}
	if call.Model != "" {
		args = append(args, "--model", call.Model)
	}
	if call.Tools != "" {
		args = append(args, "--allowedTools", call.Tools)
	}
	args = append(args, "--tools", call.Tools)
	run := exec.CommandContext(ctx, path, args...)
	run.Dir = call.Dir
	run.Stdin = strings.NewReader(call.Prompt)
	var stderr bytes.Buffer
	run.Stderr = &stderr
	raw, err := run.Output()
	if errors.Is(err, exec.ErrNotFound) {
		return &cli.Error{Code: cli.ExitFailure, Message: "claude is not at " + path, Hint: "run `slyb install` again", Cause: err}
	}
	if err != nil {
		return &cli.Error{Code: cli.ExitFailure, Message: "claude failed", Hint: strings.TrimSpace(stderr.String()), Cause: err}
	}
	var answer claudeReply
	if err := json.Unmarshal(raw, &answer); err != nil {
		return &cli.Error{Code: cli.ExitFailure, Message: "claude sent an unreadable reply", Cause: err}
	}
	if answer.IsError {
		return &cli.Error{Code: cli.ExitFailure, Message: "claude refused: " + answer.Result}
	}
	if len(answer.StructuredOutput) == 0 {
		return &cli.Error{Code: cli.ExitFailure, Message: "claude returned no structured output", Hint: answer.Result}
	}
	return json.Unmarshal(answer.StructuredOutput, out)
}
