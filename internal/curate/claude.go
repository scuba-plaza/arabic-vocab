package curate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"slices"
	"strings"
	"time"
)

type ClaudeCode struct {
	Path    string
	Model   string
	Effort  string
	Timeout time.Duration
}

type claudeResult struct {
	Type             string          `json:"type"`
	Subtype          string          `json:"subtype"`
	IsError          bool            `json:"is_error"`
	Result           string          `json:"result"`
	StructuredOutput json.RawMessage `json:"structured_output"`
	Usage            struct {
		InputTokens              int64 `json:"input_tokens"`
		OutputTokens             int64 `json:"output_tokens"`
		CacheReadInputTokens     int64 `json:"cache_read_input_tokens"`
		CacheCreationInputTokens int64 `json:"cache_creation_input_tokens"`
	} `json:"usage"`
	ModelUsage map[string]json.RawMessage `json:"modelUsage"`
}

func (c ClaudeCode) path() string {
	if c.Path != "" {
		return c.Path
	}
	return "claude"
}

func (c ClaudeCode) Args(req Request) []string {
	args := []string{
		"-p",
		"--output-format", "json",
		"--json-schema", string(req.Schema),
		"--system-prompt", req.System,
		"--tools", "",
		"--no-session-persistence",
	}
	if c.Model != "" {
		args = append(args, "--model", c.Model)
	}
	if c.Effort != "" {
		args = append(args, "--effort", c.Effort)
	}
	return args
}

func (c ClaudeCode) Complete(ctx context.Context, req Request) (*Response, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, c.path(), c.Args(req)...)
	cmd.Stdin = strings.NewReader(req.Prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	runErr := cmd.Run()

	var out claudeResult
	if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &out); err != nil || out.Type != "result" {
		detail := strings.TrimSpace(stderr.String())
		if detail == "" {
			detail = strings.TrimSpace(stdout.String())
		}
		if runErr != nil {
			return nil, fmt.Errorf("claude: %w: %s", runErr, detail)
		}
		return nil, fmt.Errorf("claude did not print a JSON result: %s", detail)
	}
	if out.IsError {
		msg := strings.TrimSpace(out.Result)
		if msg == "" {
			msg = out.Subtype
		}
		return nil, fmt.Errorf("claude: %s", msg)
	}
	if runErr != nil {
		return nil, fmt.Errorf("claude: %w: %s", runErr, strings.TrimSpace(stderr.String()))
	}
	resp := &Response{
		Output: out.StructuredOutput,
		Usage: Usage{
			Calls:      1,
			Input:      out.Usage.InputTokens,
			Output:     out.Usage.OutputTokens,
			CacheRead:  out.Usage.CacheReadInputTokens,
			CacheWrite: out.Usage.CacheCreationInputTokens,
		},
	}
	for m := range out.ModelUsage {
		resp.Usage.Models = append(resp.Usage.Models, m)
	}
	slices.Sort(resp.Usage.Models)
	return resp, nil
}
