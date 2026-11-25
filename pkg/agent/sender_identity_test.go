package agent

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/cron"
	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type identityProvider struct{}

var _ tools.SyntheticJobExecutor = (*AgentLoop)(nil)
var _ tools.SyntheticJobExecutor = (*syntheticCronExecutor)(nil)

func TestCronConstructorRequiresSyntheticExecutor(t *testing.T) {
	parameter := reflect.TypeOf(tools.NewCronTool).In(1)
	required := reflect.TypeOf((*tools.SyntheticJobExecutor)(nil)).Elem()
	if !parameter.Implements(required) {
		t.Fatalf("cron executor parameter %v does not require %v", parameter, required)
	}
}

type identityProfileRecorder struct{ ids []string }

func (r *identityProfileRecorder) AnswerTheme(id string) string {
	r.ids = append(r.ids, id)
	return ""
}

func (*identityProvider) GetDefaultModel() string { return "test" }
func (*identityProvider) Chat(_ context.Context, messages []providers.Message, _ []providers.ToolDefinition, _ string, _ map[string]interface{}) (*providers.LLMResponse, error) {
	for _, m := range messages {
		if m.Role == "tool" {
			return &providers.LLMResponse{Content: "done"}, nil
		}
	}
	return &providers.LLMResponse{ToolCalls: []providers.ToolCall{{ID: "1", Name: "identity_probe", Arguments: map[string]interface{}{"sender": "forged"}}}}, nil
}

func TestScheduledJobHasNoSenderIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &identityProvider{})
	defer al.Stop()
	profiles := &identityProfileRecorder{}
	al.SetProfileStore(profiles)
	probe := &identityProbe{results: make(chan string, 1)}
	al.RegisterTool(probe)
	job := &cron.CronJob{}
	job.Payload.Channel = "fluxer"
	job.Payload.To = "room"
	job.Payload.Message = "hello"
	tool := tools.NewCronTool(nil, al, nil, cfg.Agents.Defaults.Workspace, false)
	tool.ExecuteJob(tools.WithSenderIdentity(context.Background(), "fluxer", "inherited"), job)
	if got := <-probe.results; got != "false///" {
		t.Fatalf("scheduled job identity = %q", got)
	}
	if len(profiles.ids) != 1 || profiles.ids[0] != "fluxer:cron" {
		t.Fatalf("scheduled profile lookups = %v, want [fluxer:cron]", profiles.ids)
	}
}

type legacyCronWrapper struct{ loop *AgentLoop }

func (w *legacyCronWrapper) ProcessDirectWithChannel(ctx context.Context, content, session, channel, chat, sender string) (string, error) {
	return w.loop.ProcessDirectWithChannel(ctx, content, session, channel, chat, sender)
}

type legacyCronExecutor struct{}

type syntheticCronExecutor struct {
	inherited bool
	sender    string
}

func (*syntheticCronExecutor) ProcessDirectWithChannel(context.Context, string, string, string, string, string) (string, error) {
	return "", fmt.Errorf("unexpected direct call")
}

func (e *syntheticCronExecutor) ProcessSyntheticWithChannel(ctx context.Context, _, _, _, _, sender string) (string, error) {
	_, e.inherited = tools.SenderIdentityFromContext(ctx)
	e.sender = sender
	return "ok", nil
}

func TestCronSyntheticExecutorReceivesNoInheritedIdentity(t *testing.T) {
	executor := &syntheticCronExecutor{}
	tool := tools.NewCronTool(nil, executor, nil, t.TempDir(), false)
	job := &cron.CronJob{}
	job.Payload.Channel, job.Payload.To, job.Payload.Message = "fluxer", "room", "hello"
	ctx := tools.WithSenderIdentity(context.Background(), "fluxer", "inherited")
	if got := tool.ExecuteJob(ctx, job); got != "ok" {
		t.Fatalf("result = %q", got)
	}
	if executor.inherited || executor.sender != "cron" {
		t.Fatalf("inherited identity = %v, sender = %q", executor.inherited, executor.sender)
	}
}

func (*legacyCronExecutor) ProcessDirectWithChannel(context.Context, string, string, string, string, string) (string, error) {
	return "executed", nil
}

func TestCronRejectsExecutorsWithoutSyntheticSupport(t *testing.T) {
	required := reflect.TypeOf(tools.NewCronTool).In(1)
	for _, executor := range []any{&legacyCronWrapper{}, &legacyCronExecutor{}} {
		if reflect.TypeOf(executor).Implements(required) {
			t.Errorf("%T unexpectedly satisfies constructor contract", executor)
		}
	}
}

func TestCronAllowsNilExecutor(t *testing.T) {
	tool := tools.NewCronTool(nil, nil, nil, t.TempDir(), false)
	if got := tool.ExecuteJob(context.Background(), &cron.CronJob{}); got != "Error: scheduled agent execution requires synthetic sender support" {
		t.Fatalf("nil executor result = %q", got)
	}
}

func TestDirectAndHeartbeatHaveNoSyntheticSenderIdentity(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &identityProvider{})
	defer al.Stop()
	profiles := &identityProfileRecorder{}
	al.SetProfileStore(profiles)
	probe := &identityProbe{results: make(chan string, 2)}
	al.RegisterTool(probe)
	parent := tools.WithSenderIdentity(context.Background(), "fluxer", "stale")
	if _, err := al.ProcessDirect(parent, "hello", "direct-test"); err != nil {
		t.Fatal(err)
	}
	if got := <-probe.results; got != "false///" {
		t.Errorf("direct identity = %q", got)
	}
	if len(profiles.ids) != 1 || profiles.ids[0] != "cli:cli" {
		t.Fatalf("direct profile lookups = %v, want [cli:cli]", profiles.ids)
	}
	if _, err := al.ProcessHeartbeat(parent, "hello", "fluxer", "room"); err != nil {
		t.Fatal(err)
	}
	if got := <-probe.results; got != "false///" {
		t.Errorf("heartbeat identity = %q", got)
	}
}

type identityProbe struct{ results chan string }

func (*identityProbe) Name() string        { return "identity_probe" }
func (*identityProbe) Description() string { return "Inspect execution identity" }
func (*identityProbe) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"sender": map[string]interface{}{"type": "string"}}}
}
func (p *identityProbe) Execute(ctx context.Context, _ map[string]interface{}) *tools.ToolResult {
	id, ok := tools.SenderIdentityFromContext(ctx)
	p.results <- fmt.Sprintf("%t/%s/%s/%s", ok, id.Channel, id.RawID, id.QualifiedID())
	return tools.SilentResult("ok")
}

func TestSenderIdentityReachesToolAndDoesNotLeak(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &identityProvider{})
	defer al.Stop()
	probe := &identityProbe{results: make(chan string, 4)}
	al.RegisterTool(probe)

	for _, tc := range []struct{ channel, sender, want string }{
		{"fluxer", "123", "true/fluxer/123/fluxer:123"},
		{"fluxer", "cron", "true/fluxer/cron/fluxer:cron"},
		{"fluxer", "", "false///"},
		{"discord", "other", "true/discord/other/discord:other"},
		{"feishu", "unknown", "false///"},
		{"maixcam", "maixcam", "false///"},
	} {
		parent := tools.WithSenderIdentity(context.Background(), "old", "stale")
		_, err := al.RunJob(parent, bus.InboundMessage{Channel: tc.channel, SenderID: tc.sender, ChatID: "room", Content: "hello", SessionKey: tc.channel + tc.sender}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if got := <-probe.results; got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
}

func TestRunJobOverridesInheritedSyntheticMarker(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &identityProvider{})
	defer al.Stop()
	probe := &identityProbe{results: make(chan string, 1)}
	al.RegisterTool(probe)
	parent := tools.WithSyntheticSender(context.Background())
	_, err := al.RunJob(parent, bus.InboundMessage{Channel: "fluxer", SenderID: "123", ChatID: "room", Content: "hello", SessionKey: "real-job"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := <-probe.results; got != "true/fluxer/123/fluxer:123" {
		t.Fatalf("real job identity = %q", got)
	}
	al.HandleInbound(parent, bus.InboundMessage{Channel: "fluxer", SenderID: "456", ChatID: "room", Content: "hello", SessionKey: "inbound-job"})
	if got := <-probe.results; got != "true/fluxer/456/fluxer:456" {
		t.Fatalf("inbound job identity = %q", got)
	}
}

func TestDirectWithChannelOverridesInheritedSyntheticMarker(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Workspace = t.TempDir()
	al := NewAgentLoop(cfg, bus.NewMessageBus(), &identityProvider{})
	defer al.Stop()
	probe := &identityProbe{results: make(chan string, 1)}
	al.RegisterTool(probe)
	parent := tools.WithSyntheticSender(context.Background())
	_, err := al.ProcessDirectWithChannel(parent, "hello", "direct-real", "fluxer", "room", "123")
	if err != nil {
		t.Fatal(err)
	}
	if got := <-probe.results; got != "true/fluxer/123/fluxer:123" {
		t.Fatalf("direct sender identity = %q", got)
	}
}

func TestSpawnToolPreservesSenderIdentity(t *testing.T) {
	manager := tools.NewSubagentManager(&identityProvider{}, "test", t.TempDir(), nil)
	probe := &identityProbe{results: make(chan string, 1)}
	manager.RegisterTool(probe)
	ctx := tools.WithSenderIdentity(context.Background(), "fluxer", "123")
	spawn := tools.NewSpawnTool(manager)
	completed := make(chan struct{})
	spawn.SetCallback(func(_ context.Context, _ *tools.ToolResult) { close(completed) })
	result := spawn.Execute(ctx, map[string]interface{}{"task": "inspect"})
	if result.IsError {
		t.Fatalf("spawn failed: %s", result.ForLLM)
	}
	select {
	case got := <-probe.results:
		if got != "true/fluxer/123/fluxer:123" {
			t.Fatalf("spawn identity = %q", got)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("spawn did not execute tool")
	}
	select {
	case <-completed:
	case <-time.After(10 * time.Second):
		t.Fatal("spawn did not complete")
	}
}

func TestSubagentToolPreservesSenderIdentity(t *testing.T) {
	manager := tools.NewSubagentManager(&identityProvider{}, "test", t.TempDir(), nil)
	probe := &identityProbe{results: make(chan string, 1)}
	manager.RegisterTool(probe)
	ctx := tools.WithSenderIdentity(context.Background(), "fluxer", "123")
	result := tools.NewSubagentTool(manager).Execute(ctx, map[string]interface{}{"task": "inspect"})
	if result.IsError {
		t.Fatalf("subagent failed: %s", result.ForLLM)
	}
	if got := <-probe.results; got != "true/fluxer/123/fluxer:123" {
		t.Fatalf("subagent identity = %q", got)
	}
}

type overlappingIdentityProbe struct {
	entered chan string
	release <-chan struct{}
	job     string
}

func (*overlappingIdentityProbe) Name() string        { return "identity_probe" }
func (*overlappingIdentityProbe) Description() string { return "Inspect execution identity" }
func (*overlappingIdentityProbe) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{"sender": map[string]interface{}{"type": "string"}}}
}
func (p *overlappingIdentityProbe) Execute(ctx context.Context, _ map[string]interface{}) *tools.ToolResult {
	id, ok := tools.SenderIdentityFromContext(ctx)
	p.entered <- fmt.Sprintf("%s:%t/%s/%s", p.job, ok, id.Channel, id.RawID)
	<-p.release
	id, ok = tools.SenderIdentityFromContext(ctx)
	p.entered <- fmt.Sprintf("%s:%t/%s/%s", p.job, ok, id.Channel, id.RawID)
	return tools.SilentResult("ok")
}

func TestSenderIdentityIsolatedAcrossOverlappingAgentExecutions(t *testing.T) {
	entered := make(chan string, 4)
	release := make(chan struct{})
	done := make(chan error, 2)
	for _, raw := range []string{"one", "two"} {
		cfg := config.DefaultConfig()
		cfg.Agents.Defaults.Workspace = t.TempDir()
		al := NewAgentLoop(cfg, bus.NewMessageBus(), &identityProvider{})
		defer al.Stop()
		al.RegisterTool(&overlappingIdentityProbe{entered: entered, release: release, job: raw})
		go func(raw string) {
			_, err := al.ProcessDirectWithChannel(context.Background(), "hello", raw, "fluxer", "room", raw)
			done <- err
		}(raw)
	}
	received := map[string]int{}
	for i := 0; i < 2; i++ {
		select {
		case value := <-entered:
			received[value]++
		case <-time.After(10 * time.Second):
			close(release)
			t.Fatal("both tools did not overlap")
		}
	}
	close(release)
	for i := 0; i < 2; i++ {
		received[<-entered]++
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	if received["one:true/fluxer/one"] != 2 || received["two:true/fluxer/two"] != 2 || len(received) != 2 {
		t.Fatalf("overlapping identities = %v", received)
	}
}
