package channels

import "strings"

const helpDocumentationURL = "https://sciclaw.dev/docs.html"

const helpMessage = "sciClaw help\n\n" +
	"Ask a question or describe a task in a message. Attach files when they are relevant, and reply to a sciClaw response to continue that conversation.\n\n" +
	"Commands:\n" +
	"• help — show this message\n" +
	"• /btw <question> — ask a quick read-only side question (Discord)\n" +
	"• /skill <name> <task> — run a task with an explicit skill (Discord)\n" +
	"• /theme <clear|formal|brief> — choose your answer style (Discord)\n\n" +
	"Documentation: " + helpDocumentationURL

func isHelpCommand(content string) bool {
	switch strings.ToLower(strings.TrimSpace(content)) {
	case "help", "/help", "!help":
		return true
	default:
		return false
	}
}
