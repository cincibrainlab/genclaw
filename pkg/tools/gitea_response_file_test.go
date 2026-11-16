package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
)

func TestGiteaMCPErrorStreamsAndWriteReconciliation(t *testing.T) {
	bin := t.TempDir()
	script := `#!/bin/sh
printf '{"isError":true,"message":"stdout-canary"}'
printf 'stderr-canary' >&2
exit "$EXIT_CODE"
`
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	tool := NewGiteaMCPTool(t.TempDir(), true)
	for _, tc := range []struct{ name, exit string }{{"issue_read", "1"}, {"create_organization", "0"}, {"repo_write", "0"}} {
		t.Setenv("EXIT_CODE", tc.exit)
		result := tool.Execute(context.Background(), map[string]interface{}{"tool": tc.name, "arguments": map[string]interface{}{}})
		if !result.IsError || !strings.Contains(result.ForLLM, "stdout-canary") {
			t.Fatalf("%s lost stdout/status: %q", tc.name, result.ForLLM)
		}
		if tc.exit == "1" && !strings.Contains(result.ForLLM, "stderr-canary") {
			t.Fatalf("lost stderr: %q", result.ForLLM)
		}
		if tc.exit == "0" && !strings.Contains(strings.ToLower(result.ForLLM), "reconcile") {
			t.Fatalf("%s lacks reconciliation: %q", tc.name, result.ForLLM)
		}
	}
}

func TestToolLoopAutomaticPaginationArtifacts(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(fmt.Sprint("fail=", fail), func(t *testing.T) {
			workspace, bin := t.TempDir(), t.TempDir()
			script := `#!/usr/bin/python3
import json, os, sys
args = json.load(sys.stdin)
if args['page'] == 1:
 print(json.dumps({'content':[{'type':'text','text':json.dumps([{'value':'page-one-' + 'x'*15000}])}]}))
elif os.environ['FAIL_PAGE'] == 'true':
 print('invalid-second-page-canary')
else:
 print(json.dumps({'content':[{'type':'text','text':'[]'}]}))
`
			if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte(script), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin)
			t.Setenv("FAIL_PAGE", fmt.Sprint(fail))
			registry := NewToolRegistry()
			registry.Register(NewGiteaMCPTool(workspace, true))
			provider := &toolLoopEchoProvider{toolCall: &providers.ToolCall{ID: "1", Name: "atomicqms_mcp", Arguments: map[string]interface{}{"tool": "project_read", "arguments": map[string]interface{}{"method": "list"}}}}
			result, err := RunToolLoop(context.Background(), ToolLoopConfig{Provider: provider, Tools: registry, ToolResultMaxChars: 64}, nil, "", "")
			if err != nil {
				t.Fatal(err)
			}
			files, err := os.ReadDir(workspace)
			if err != nil || len(files) != 1 {
				t.Fatalf("artifact missing: %v", err)
			}
			if len(result.Content) >= 1000 || !strings.Contains(result.Content, files[0].Name()) || !strings.Contains(result.Content, "bytes") || !strings.Contains(result.Content, "gitea-read-file") {
				t.Fatalf("invalid metadata: %q", result.Content)
			}
			data, err := os.ReadFile(filepath.Join(workspace, files[0].Name()))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "page-one-") {
				t.Fatal("first page lost")
			}
			if fail && (!strings.Contains(string(data), "invalid-second-page-canary") || !strings.Contains(strings.ToLower(result.Content), "error")) {
				t.Fatalf("incomplete pagination not preserved and reported: %s", result.Content)
			}
		})
	}
}

func TestToolLoopSavesBothErrorStreams(t *testing.T) {
	workspace, bin := t.TempDir(), t.TempDir()
	payload := "stdout-private-" + strings.Repeat("x", 15000)
	if err := os.WriteFile(filepath.Join(bin, "out"), []byte(payload), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte("#!/bin/sh\n/bin/cat \"$MCP_FIXTURE\"\nprintf 'stderr-canary' >&2\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MCP_FIXTURE", filepath.Join(bin, "out"))
	registry := NewToolRegistry()
	registry.Register(NewGiteaMCPTool(workspace, true))
	provider := &toolLoopEchoProvider{toolCall: &providers.ToolCall{ID: "1", Name: "atomicqms_mcp", Arguments: map[string]interface{}{"tool": "issue_read", "arguments": map[string]interface{}{}}}}
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{Provider: provider, Tools: registry, ToolResultMaxChars: 64}, nil, "", "")
	if err != nil {
		t.Fatal(err)
	}
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 1 {
		t.Fatal("missing private error artifact")
	}
	if !strings.HasSuffix(files[0].Name(), ".txt") || !strings.Contains(result.Content, files[0].Name()) || strings.Contains(result.Content, "stdout-private") || len(result.Content) > 1000 {
		t.Fatalf("unsafe metadata: %s", result.Content)
	}
	got, err := os.ReadFile(filepath.Join(workspace, files[0].Name()))
	if err != nil || !strings.Contains(string(got), payload) || !strings.Contains(string(got), "stderr-canary") {
		t.Fatal("error streams not preserved")
	}
}

func TestToolLoopSmallLimitPreservesSaveFailure(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte("#!/bin/sh\nprintf '%0100d' 0\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	registry := NewToolRegistry()
	registry.Register(NewGiteaMCPTool(filepath.Join(bin, "missing"), true))
	provider := &toolLoopEchoProvider{toolCall: &providers.ToolCall{ID: "1", Name: "atomicqms_mcp", Arguments: map[string]interface{}{"tool": "project_read", "arguments": map[string]interface{}{"method": "list", "page": 1}}}}
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{Provider: provider, Tools: registry, ToolResultMaxChars: 64}, nil, "", "")
	if err != nil || len(result.Content) > 1000 || !strings.Contains(result.Content, "reconcile") || strings.Contains(result.Content, "truncated") {
		t.Fatalf("save failure truncated: %v %s", err, result.Content)
	}
}

type failingResponseWriter struct{ short, closeFail bool }

func (w *failingResponseWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	if w.closeFail {
		return len(p), nil
	}
	return 0, errors.New("write failed")
}
func (w *failingResponseWriter) Close() error {
	if w.closeFail {
		return errors.New("close failed")
	}
	return nil
}

func TestMCPResponseWriteFailures(t *testing.T) {
	for _, w := range []*failingResponseWriter{{}, {short: true}, {closeFail: true}} {
		if err := writeMCPResponse(w, "some response"); err == nil {
			t.Fatal("write or close failure accepted")
		}
	}
}

func TestMCPResponseSaveFailureAndErrorStatus(t *testing.T) {
	registry := NewToolRegistry()
	registry.Register(NewGiteaMCPTool(filepath.Join(t.TempDir(), "missing"), true))
	result, _ := PrepareGiteaMCPResult(registry, "atomicqms_mcp", NewToolResult(strings.Repeat("secret", 3000)), 12000)
	if !result.IsError || strings.Contains(result.ForLLM, "secret") || !strings.Contains(result.ForLLM, "reconcile") {
		t.Fatalf("unsafe failure: %#v", result)
	}
	dir := t.TempDir()
	registry.Register(NewGiteaMCPTool(dir, true))
	for i := 0; i < 2; i++ {
		result, _ = PrepareGiteaMCPResult(registry, "atomicqms_mcp", ErrorResult(strings.Repeat("x", 13000)), 12000)
		if !result.IsError || !strings.Contains(result.ForLLM, "reconcile") {
			t.Fatalf("error status lost: %#v", result)
		}
	}
	files, err := os.ReadDir(dir)
	if err != nil || len(files) != 2 {
		t.Fatalf("unique files: %v, %d", err, len(files))
	}
}

func TestMCPResponseCreateDoesNotFollowOrOverwrite(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	target := filepath.Join(outside, "target")
	if err := os.WriteFile(target, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	for _, name := range []string{"existing", "link"} {
		if name == "existing" {
			err = os.WriteFile(filepath.Join(dir, name), []byte("original"), 0600)
		} else {
			err = os.Symlink(target, filepath.Join(dir, name))
		}
		if err != nil {
			t.Fatal(err)
		}
		if err = createMCPResponse(root, name, "replacement"); err == nil {
			t.Fatal("existing path accepted")
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || string(got) != "original" {
			t.Fatal("existing path overwritten")
		}
	}
}

func TestToolLoopSavesProjectListInsteadOfRejecting(t *testing.T) {
	workspace, bin := t.TempDir(), t.TempDir()
	response := `{"content":[{"type":"text","text":"` + strings.Repeat("x", 15000) + `"}]}`
	if err := os.WriteFile(filepath.Join(bin, "response"), []byte(response), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte("#!/bin/sh\n/bin/cat \"$MCP_FIXTURE\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MCP_FIXTURE", filepath.Join(bin, "response"))
	registry := NewToolRegistry()
	registry.Register(NewGiteaMCPTool(workspace, true))
	provider := &toolLoopEchoProvider{toolCall: &providers.ToolCall{ID: "1", Name: "atomicqms_mcp", Arguments: map[string]interface{}{"tool": "project_read", "arguments": map[string]interface{}{"method": "list", "page": 1}}}}
	result, err := RunToolLoop(context.Background(), ToolLoopConfig{Provider: provider, Tools: registry, ToolResultMaxChars: 12000}, nil, "", "")
	if err != nil || strings.Contains(result.Content, "no results were returned") {
		t.Fatalf("project list rejected: %v %s", err, result.Content)
	}
	files, err := os.ReadDir(workspace)
	if err != nil || len(files) != 1 {
		t.Fatal("missing response file")
	}
	got, err := os.ReadFile(filepath.Join(workspace, files[0].Name()))
	if err != nil || string(got) != response {
		t.Fatal("project list not preserved")
	}
}

func TestGiteaMCPJSONErrorStatus(t *testing.T) {
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte("#!/bin/sh\nprintf '{\"isError\":true,\"content\":[]}'\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	result := NewGiteaMCPTool(t.TempDir(), true).Execute(context.Background(), map[string]interface{}{"tool": "get_file_contents", "arguments": map[string]interface{}{}})
	if !result.IsError {
		t.Fatal("MCP isError treated as success")
	}
}

// The real registered CLI result must reach the workspace intact, not the model.
func TestToolLoopSavesLargeMCPResponse(t *testing.T) {
	workspace, bin := t.TempDir(), t.TempDir()
	encoded := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("secret-binary-data", 150000)))
	inner, _ := json.Marshal(map[string]any{"content": encoded, "encoding": "base64"})
	response, _ := json.Marshal(map[string]any{"structuredContent": map[string]any{"content": encoded, "encoding": "base64"}, "content": []any{map[string]any{"type": "text", "text": string(inner)}}})
	fixture := filepath.Join(bin, "response")
	if err := os.WriteFile(fixture, response, 0600); err != nil {
		t.Fatal(err)
	}
	script := "#!/bin/sh\n/bin/cat \"$MCP_FIXTURE\"\n"
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MCP_FIXTURE", fixture)
	registry := NewToolRegistry()
	registry.Register(NewGiteaMCPTool(workspace, true))
	for _, limit := range []int{4000, 12000, 0} {
		provider := &toolLoopEchoProvider{toolCall: &providers.ToolCall{ID: "1", Name: "atomicqms_mcp", Arguments: map[string]interface{}{"tool": "get_file_contents", "arguments": map[string]interface{}{}}}}
		result, err := RunToolLoop(context.Background(), ToolLoopConfig{Provider: provider, Tools: registry, ToolResultMaxChars: limit}, nil, "", "")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Content) > 1000 || strings.Contains(result.Content, encoded[:100]) {
			t.Fatalf("unbounded model output: %d", len(result.Content))
		}
		files, err := os.ReadDir(workspace)
		if err != nil {
			t.Fatal(err)
		}
		if len(files) != 1 {
			t.Fatalf("expected new saved response, got %d", len(files))
		}
		path := filepath.Join(workspace, files[0].Name())
		if !strings.Contains(result.Content, files[0].Name()) {
			t.Fatalf("missing relative file: %s", result.Content)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(response) {
			t.Fatal("saved response differs")
		}
		info, _ := os.Stat(path)
		if info.Mode().Perm() != 0600 {
			t.Fatalf("mode %v", info.Mode())
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
}
