package curate

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

type fakeClaude struct {
	path  string
	args  string
	stdin string
}

func newFakeClaude(t *testing.T, stdout string, exit int) fakeClaude {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake claude is a shell script")
	}
	dir := t.TempDir()
	f := fakeClaude{path: filepath.Join(dir, "claude"), args: filepath.Join(dir, "args"), stdin: filepath.Join(dir, "stdin")}
	script := "#!/bin/sh\n" +
		"for a in \"$@\"; do printf '%s\\0' \"$a\"; done > '" + f.args + "'\n" +
		"cat > '" + f.stdin + "'\n" +
		"cat <<'JSON'\n" + stdout + "\nJSON\n" +
		"exit " + string(rune('0'+exit)) + "\n"
	if err := os.WriteFile(f.path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f fakeClaude) received(t *testing.T) ([]string, string) {
	t.Helper()
	rawArgs, err := os.ReadFile(f.args)
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := os.ReadFile(f.stdin)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimSuffix(string(rawArgs), "\x00"), "\x00"), string(stdin)
}

func TestClaudeCodeRunsPrintModeWithTheSchema(t *testing.T) {
	fake := newFakeClaude(t, `{"type":"result","subtype":"success","is_error":false,"result":"{}","structured_output":{"cards":[]},"usage":{"input_tokens":2,"output_tokens":113,"cache_read_input_tokens":100,"cache_creation_input_tokens":1713},"modelUsage":{"test-model":{"inputTokens":2}}}`, 0)
	c := ClaudeCode{Path: fake.path, Model: "test-model", Effort: "high"}
	resp, err := c.Complete(context.Background(), Request{System: "guide\nsecond line", Prompt: "the drafts", Schema: []byte(`{"type":"object"}`)})
	if err != nil {
		t.Fatal(err)
	}
	args, stdin := fake.received(t)
	want := []string{"-p", "--output-format", "json", "--json-schema", `{"type":"object"}`, "--system-prompt", "guide\nsecond line", "--tools", "", "--no-session-persistence", "--model", "test-model", "--effort", "high"}
	if !slices.Equal(args, want) {
		t.Errorf("args = %q\nwant   %q", args, want)
	}
	if stdin != "the drafts" {
		t.Errorf("stdin = %q", stdin)
	}
	if string(resp.Output) != `{"cards":[]}` {
		t.Errorf("output = %s", resp.Output)
	}
	u := resp.Usage
	if u.Calls != 1 || u.Input != 2 || u.Output != 113 || u.CacheRead != 100 || u.CacheWrite != 1713 || !slices.Equal(u.Models, []string{"test-model"}) {
		t.Errorf("usage = %+v", u)
	}
}

func TestClaudeCodeLeavesModelToClaudeCode(t *testing.T) {
	fake := newFakeClaude(t, `{"type":"result","is_error":false,"structured_output":{"cards":[]}}`, 0)
	if _, err := (ClaudeCode{Path: fake.path}).Complete(context.Background(), Request{Schema: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	args, _ := fake.received(t)
	if slices.Contains(args, "--model") || slices.Contains(args, "--effort") {
		t.Errorf("args = %q", args)
	}
}

func TestClaudeCodeReportsErrorResults(t *testing.T) {
	fake := newFakeClaude(t, `{"type":"result","subtype":"success","is_error":true,"result":"Claude AI usage limit reached|1759000000"}`, 1)
	_, err := ClaudeCode{Path: fake.path}.Complete(context.Background(), Request{Schema: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "usage limit reached") {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeCodeReportsOutputThatIsNotJSON(t *testing.T) {
	fake := newFakeClaude(t, "Invalid API key · Please run /login", 1)
	_, err := ClaudeCode{Path: fake.path}.Complete(context.Background(), Request{Schema: []byte(`{}`)})
	if err == nil || !strings.Contains(err.Error(), "Please run /login") {
		t.Fatalf("err = %v", err)
	}
}

func TestClaudeCodeReportsAMissingBinary(t *testing.T) {
	_, err := ClaudeCode{Path: filepath.Join(t.TempDir(), "claude")}.Complete(context.Background(), Request{Schema: []byte(`{}`)})
	if err == nil {
		t.Fatal("expected an error for a missing binary")
	}
}
