package channels

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/slack-go/slack/socketmode"
)

func TestSlackAppMentionHelpUsesOutboundOnly(t *testing.T) {
	mb := bus.NewMessageBus()
	ch := &SlackChannel{BaseChannel: NewBaseChannel("slack", config.SlackConfig{}, mb, []string{"u1"}), ctx: context.Background(), botUserID: "bot-1"}
	ch.setRunning(true)
	var sent []bus.OutboundMessage
	reactions := 0
	ch.sendFn = func(_ context.Context, msg bus.OutboundMessage) error { sent = append(sent, msg); return nil }
	ch.addReactionFn = func(string, string) { reactions++ }
	ch.handleAppMention(&slackevents.AppMentionEvent{User: "u1", Channel: "C1", TimeStamp: "t1", Text: "<@bot-1> help"})
	if len(sent) != 1 || sent[0].Content != helpMessage || reactions != 0 || lenPendingAcks(ch) != 0 {
		t.Fatalf("help path sent=%+v reactions=%d pending=%d", sent, reactions, lenPendingAcks(ch))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatal("Slack help must not publish inbound work")
	}
}

func TestSlackAppMentionHelpPreservesAllowlistAndNormalTraffic(t *testing.T) {
	mb := bus.NewMessageBus()
	ch := &SlackChannel{BaseChannel: NewBaseChannel("slack", config.SlackConfig{}, mb, []string{"u1"}), ctx: context.Background(), botUserID: "bot-1"}
	ch.setRunning(true)
	ch.sendFn = func(context.Context, bus.OutboundMessage) error { return nil }
	ch.addReactionFn = func(string, string) {}
	ch.handleAppMention(&slackevents.AppMentionEvent{User: "denied", Channel: "C1", TimeStamp: "t1", Text: "<@bot-1> help"})
	ch.handleAppMention(&slackevents.AppMentionEvent{User: "u1", Channel: "C1", TimeStamp: "t2", Text: "<@bot-1> normal request"})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	in, ok := mb.ConsumeInbound(ctx)
	if !ok || !strings.Contains(in.Content, "normal request") {
		t.Fatalf("expected normal app mention: %+v ok=%t", in, ok)
	}
}

func TestSlackMessageEventHelpUsesExactOutboundTarget(t *testing.T) {
	mb := bus.NewMessageBus()
	ch := &SlackChannel{BaseChannel: NewBaseChannel("slack", config.SlackConfig{}, mb, []string{"u1"}), ctx: context.Background(), botUserID: "bot-1"}
	ch.setRunning(true)
	var sent []bus.OutboundMessage
	ch.sendFn = func(_ context.Context, msg bus.OutboundMessage) error { sent = append(sent, msg); return nil }
	reactions := 0
	ch.addReactionFn = func(string, string) { reactions++ }
	ch.handleMessageEvent(&slackevents.MessageEvent{User: "u1", Channel: "C1", ThreadTimeStamp: "thread-1", TimeStamp: "message-1", Text: "help"})
	if len(sent) != 1 || sent[0].Channel != "slack" || sent[0].ChatID != "C1/thread-1" || sent[0].Content != helpMessage || reactions != 0 || lenPendingAcks(ch) != 0 {
		t.Fatalf("message help sent=%+v reactions=%d pending=%d", sent, reactions, lenPendingAcks(ch))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatal("Slack message help must not publish inbound work")
	}
}

func TestSlackSlashHelpUsesExactOutboundTargetAndDeniedUserDoesNothing(t *testing.T) {
	mb := bus.NewMessageBus()
	ch := &SlackChannel{BaseChannel: NewBaseChannel("slack", config.SlackConfig{}, mb, []string{"u1"}), ctx: context.Background()}
	ch.setRunning(true)
	var sent []bus.OutboundMessage
	ch.sendFn = func(_ context.Context, msg bus.OutboundMessage) error { sent = append(sent, msg); return nil }
	ch.handleSlashCommand(socketmode.Event{Data: slack.SlashCommand{UserID: "u1", ChannelID: "C2", Command: "/help"}})
	if len(sent) != 1 || sent[0].Channel != "slack" || sent[0].ChatID != "C2" || sent[0].Content != helpMessage {
		t.Fatalf("slash help sent=%+v", sent)
	}
	ch.handleSlashCommand(socketmode.Event{Data: slack.SlashCommand{UserID: "denied", ChannelID: "C3", Command: "/help"}})
	if len(sent) != 1 {
		t.Fatalf("denied slash help must not send, got=%+v", sent)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, ok := mb.ConsumeInbound(ctx); ok {
		t.Fatal("Slack slash help must not publish inbound work")
	}
}

func lenPendingAcks(ch *SlackChannel) int {
	n := 0
	ch.pendingAcks.Range(func(_, _ any) bool { n++; return true })
	return n
}

func TestParseSlackChatID(t *testing.T) {
	tests := []struct {
		name       string
		chatID     string
		wantChanID string
		wantThread string
	}{
		{
			name:       "channel only",
			chatID:     "C123456",
			wantChanID: "C123456",
			wantThread: "",
		},
		{
			name:       "channel with thread",
			chatID:     "C123456/1234567890.123456",
			wantChanID: "C123456",
			wantThread: "1234567890.123456",
		},
		{
			name:       "DM channel",
			chatID:     "D987654",
			wantChanID: "D987654",
			wantThread: "",
		},
		{
			name:       "empty string",
			chatID:     "",
			wantChanID: "",
			wantThread: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			chanID, threadTS := parseSlackChatID(tt.chatID)
			if chanID != tt.wantChanID {
				t.Errorf("parseSlackChatID(%q) channelID = %q, want %q", tt.chatID, chanID, tt.wantChanID)
			}
			if threadTS != tt.wantThread {
				t.Errorf("parseSlackChatID(%q) threadTS = %q, want %q", tt.chatID, threadTS, tt.wantThread)
			}
		})
	}
}

func TestStripBotMention(t *testing.T) {
	ch := &SlackChannel{botUserID: "U12345BOT"}

	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "mention at start",
			input: "<@U12345BOT> hello there",
			want:  "hello there",
		},
		{
			name:  "mention in middle",
			input: "hey <@U12345BOT> can you help",
			want:  "hey  can you help",
		},
		{
			name:  "no mention",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "only mention",
			input: "<@U12345BOT>",
			want:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ch.stripBotMention(tt.input)
			if got != tt.want {
				t.Errorf("stripBotMention(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestNewSlackChannel(t *testing.T) {
	msgBus := bus.NewMessageBus()

	t.Run("missing bot token", func(t *testing.T) {
		cfg := config.SlackConfig{
			BotToken: "",
			AppToken: "xapp-test",
		}
		_, err := NewSlackChannel(cfg, msgBus)
		if err == nil {
			t.Error("expected error for missing bot_token, got nil")
		}
	})

	t.Run("missing app token", func(t *testing.T) {
		cfg := config.SlackConfig{
			BotToken: "xoxb-test",
			AppToken: "",
		}
		_, err := NewSlackChannel(cfg, msgBus)
		if err == nil {
			t.Error("expected error for missing app_token, got nil")
		}
	})

	t.Run("valid config", func(t *testing.T) {
		cfg := config.SlackConfig{
			BotToken:  "xoxb-test",
			AppToken:  "xapp-test",
			AllowFrom: []string{"U123"},
		}
		ch, err := NewSlackChannel(cfg, msgBus)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if ch.Name() != "slack" {
			t.Errorf("Name() = %q, want %q", ch.Name(), "slack")
		}
		if ch.IsRunning() {
			t.Error("new channel should not be running")
		}
	})
}

func TestSlackChannelIsAllowed(t *testing.T) {
	msgBus := bus.NewMessageBus()

	t.Run("empty allowlist allows all", func(t *testing.T) {
		cfg := config.SlackConfig{
			BotToken:  "xoxb-test",
			AppToken:  "xapp-test",
			AllowFrom: []string{},
		}
		ch, _ := NewSlackChannel(cfg, msgBus)
		if !ch.IsAllowed("U_ANYONE") {
			t.Error("empty allowlist should allow all users")
		}
	})

	t.Run("allowlist restricts users", func(t *testing.T) {
		cfg := config.SlackConfig{
			BotToken:  "xoxb-test",
			AppToken:  "xapp-test",
			AllowFrom: []string{"U_ALLOWED"},
		}
		ch, _ := NewSlackChannel(cfg, msgBus)
		if !ch.IsAllowed("U_ALLOWED") {
			t.Error("allowed user should pass allowlist check")
		}
		if ch.IsAllowed("U_BLOCKED") {
			t.Error("non-allowed user should be blocked")
		}
	})
}
