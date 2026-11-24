package channels

import (
	"strings"
	"testing"
)

func TestIsHelpCommand(t *testing.T) {
	for _, input := range []string{"help", " HELP ", "/help", "!help"} {
		if !isHelpCommand(input) {
			t.Errorf("isHelpCommand(%q) = false, want true", input)
		}
	}

	for _, input := range []string{"", "help me", "what is help", "/help now"} {
		if isHelpCommand(input) {
			t.Errorf("isHelpCommand(%q) = true, want false", input)
		}
	}
}

func TestHelpMessageIncludesSupportedCommandsAndDocumentation(t *testing.T) {
	for _, want := range []string{"/btw", "/skill", "/theme", helpDocumentationURL} {
		if !strings.Contains(helpMessage, want) {
			t.Errorf("helpMessage missing %q", want)
		}
	}
}

func TestTelegramHelpCommandAcceptsOnlyOwnBotAddress(t *testing.T) {
	for _, input := range []string{"help", "/help", "!help", "/help@SciClawBot"} {
		if !isTelegramHelpCommand(input, "sciclawbot") {
			t.Errorf("isTelegramHelpCommand(%q) = false", input)
		}
	}
	if isTelegramHelpCommand("/help@otherbot", "sciclawbot") {
		t.Fatal("help for another bot should remain normal traffic")
	}
}

func TestBuildHelpSlashCommand(t *testing.T) {
	cmd := buildHelpSlashCommand()
	if cmd.Name != "help" || cmd.Description == "" || len(cmd.Options) != 0 {
		t.Fatalf("unexpected help command: %+v", cmd)
	}
}
