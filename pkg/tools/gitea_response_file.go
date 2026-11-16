package tools

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// PrepareGiteaMCPResult saves oversized responses using the executing registry's workspace.
func PrepareGiteaMCPResult(registry *ToolRegistry, name string, result *ToolResult, limit int) (*ToolResult, bool) {
	if registry == nil || name != "atomicqms_mcp" || result == nil {
		return result, false
	}
	registered, ok := registry.Get(name)
	tool, okTool := registered.(*GiteaMCPTool)
	if !ok || !okTool {
		return result, false
	}
	if limit <= 0 {
		limit = 12000
	}
	if len(result.ForLLM) <= limit {
		return result, false
	}
	failed := func() *ToolResult {
		return ErrorResult("Could not save the complete Gitea MCP response. The call already ran; reconcile remote state before retrying mutations.")
	}
	if tool.workspace == "" {
		return failed(), true
	}
	root, err := os.OpenRoot(tool.workspace)
	if err != nil {
		return failed(), true
	}
	defer root.Close()
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return failed(), true
	}
	ext := ".json"
	if result.IsError {
		ext = ".txt"
	}
	path := "mcp-response-" + hex.EncodeToString(nonce[:]) + ext
	if err := createMCPResponse(root, path, result.ForLLM); err != nil {
		return failed(), true
	}
	copy := *result
	copy.ForLLM = fmt.Sprintf("Gitea MCP response saved to workspace file %s (%d bytes). Use the gitea-read-file skill to inspect or decode locally; do not print the JSON or base64.", path, len(result.ForLLM))
	if result.IsError {
		copy.ForLLM = "Gitea MCP reported an error or unknown outcome; results may be incomplete; reconcile remote state before retrying mutations. " + copy.ForLLM
	}
	copy.ForUser = ""
	copy.Err = nil
	return &copy, true
}

func createMCPResponse(root *os.Root, path, content string) error {
	f, err := root.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	if err := writeMCPResponse(f, content); err != nil {
		_ = root.Remove(path)
		return err
	}
	return nil
}

func writeMCPResponse(f io.WriteCloser, content string) error {
	n, err := io.WriteString(f, content)
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if n != len(content) {
		return io.ErrShortWrite
	}
	return closeErr
}
