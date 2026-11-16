package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

func TestMainLoopSavesMCPResponseInRegisteredWorkspace(t *testing.T) {
	workspace, routed, bin := t.TempDir(), t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(workspace, "hooks.yaml"), []byte("audit:\n  enabled: false\n"), 0644); err != nil {
		t.Fatal(err)
	}
	item := map[string]interface{}{"content": strings.Repeat("A", 3_500_000), "encoding": "base64", "size": 2625000}
	inner, _ := json.Marshal(item)
	payload, _ := json.Marshal(map[string]interface{}{"structuredContent": item, "content": []interface{}{map[string]interface{}{"type": "text", "text": string(inner)}}})
	fixture := filepath.Join(bin, "response")
	if err := os.WriteFile(fixture, payload, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mcp-server-cli"), []byte("#!/bin/sh\n/bin/cat \"$MCP_FIXTURE\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	t.Setenv("MCP_FIXTURE", fixture)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = workspace
	for _, local := range []bool{false, true} {
		provider := &scriptedProvider{responses: []*providers.LLMResponse{{ToolCalls: []providers.ToolCall{{ID: "1", Name: "atomicqms_mcp", Arguments: map[string]interface{}{"tool": "get_file_contents", "arguments": map[string]interface{}{}}}}}, {Content: "done"}}}
		al := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
		al.localMode = local
		al.tools.Register(tools.NewGiteaMCPTool(routed, true))
		if _, err := al.ProcessDirectWithChannel(context.Background(), "read", "test-session", "test", "chat", "user"); err != nil {
			t.Fatal(err)
		}
		files, err := os.ReadDir(routed)
		if err != nil || len(files) != 1 {
			t.Fatalf("routed workspace: %v, files %d", err, len(files))
		}
		got, err := os.ReadFile(filepath.Join(routed, files[0].Name()))
		if err != nil || string(got) != string(payload) {
			t.Fatal("saved response differs")
		}
		var output string
		for _, m := range provider.calls[1] {
			if m.Role == "tool" {
				output = m.Content
			}
		}
		if len(output) > 1000 || !strings.Contains(output, files[0].Name()) || strings.Contains(output, strings.Repeat("A", 100)) {
			t.Fatalf("invalid tool result length %d", len(output))
		}
		if err := os.Remove(filepath.Join(routed, files[0].Name())); err != nil {
			t.Fatal(err)
		}
	}
}
