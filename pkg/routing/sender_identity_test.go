package routing

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/agent"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type routedIdentityProvider struct{}

func (*routedIdentityProvider) GetDefaultModel() string { return "test" }
func (*routedIdentityProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	for _, m := range messages {
		if m.Role == "tool" {
			return &providers.LLMResponse{Content: "done"}, nil
		}
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "1", Name: "identity_probe", Arguments: map[string]interface{}{"job": messages[len(messages)-1].Content}}}}, nil
}

type routedIdentityProbe struct {
	entered chan string
	release <-chan struct{}
}

func (*routedIdentityProbe) Name() string        { return "identity_probe" }
func (*routedIdentityProbe) Description() string { return "Inspect sender identity" }
func (*routedIdentityProbe) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"job": map[string]interface{}{"type": "string"}}}
}
func (p *routedIdentityProbe) Execute(ctx context.Context, args map[string]interface{}) *tools.ToolResult {
	record := func() {
		id, ok := tools.SenderIdentityFromContext(ctx)
		p.entered <- fmt.Sprintf("%s:%t/%s/%s", args["job"], ok, id.Channel, id.RawID)
	}
	record()
	select {
	case <-p.release:
		record()
	case <-ctx.Done():
		return tools.ErrorResult("cancelled")
	}
	return tools.SilentResult("ok")
}

func TestJobManagerOverlappingSenderIdentities(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	mb := bus.NewMessageBus()
	defer mb.Close()
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	entered := make(chan string, 4)
	release := make(chan struct{})
	probe := &routedIdentityProbe{entered: entered, release: release}
	main := agent.NewAgentLoop(cfg, mb, &routedIdentityProvider{})
	side := agent.NewAgentLoopWithOptions(cfg, mb, &routedIdentityProvider{}, agent.LoopOptions{ToolProfile: agent.ToolProfileSideLane})
	defer main.Stop()
	defer side.Stop()
	main.RegisterTool(probe)
	side.RegisterTool(probe)
	jm, err := NewJobManager(filepath.Join(t.TempDir(), "jobs.json"), config.JobsConfig{MaxConcurrent: 2, AllowBTWDuringWrite: true}, mb, &fakeProgressMessenger{}, func(LoopTarget) (JobRunner, error) { return main, nil })
	if err != nil {
		t.Fatal(err)
	}
	jm.SetSideLaneResolver(func(LoopTarget) (JobRunner, error) { return side, nil })
	target := LoopTarget{Workspace: cfg.Agents.Defaults.Workspace}
	submit := func(sender, content string) {
		t.Helper()
		if err := jm.Submit(ctx, target, bus.InboundMessage{Channel: "discord", ChatID: "room", SenderID: sender, Content: content, SessionKey: sender}); err != nil {
			t.Fatal(err)
		}
	}
	receive := func() string {
		t.Helper()
		select {
		case value := <-entered:
			return value
		case <-ctx.Done():
			t.Fatal("timed out waiting for routed identity probe")
			return ""
		}
	}
	submit("one", "one")
	first := receive()
	submit("two", "/btw two")
	second := receive()
	close(release)
	values := []string{first, second, receive(), receive()}
	counts := map[string]int{}
	for _, value := range values {
		switch {
		case strings.Contains(value, "one:true/discord/one"):
			counts["one"]++
		case strings.Contains(value, "two:true/discord/two"):
			counts["two"]++
		default:
			t.Fatalf("unexpected identity observation %q", value)
		}
	}
	if counts["one"] != 2 || counts["two"] != 2 {
		t.Fatalf("identity observations = %v", values)
	}
	waitForNoActiveJobs(t, jm, target.normalized().key())
}
