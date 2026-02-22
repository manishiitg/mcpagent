package codeexec

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
)

// ShellCommandParams is the JSON-schema for execute_shell_command.
var ShellCommandParams = map[string]interface{}{
	"type": "object",
	"properties": map[string]interface{}{
		"command": map[string]interface{}{
			"type":        "string",
			"description": "The shell command to execute",
		},
	},
	"required": []string{"command"},
}

// ShellCommandDescription is the tool description for execute_shell_command.
const ShellCommandDescription = "Execute a shell command and return stdout, stderr, and exit code. Use this to run code, call HTTP endpoints with curl, or perform any shell operation."

// ExecuteShellCommand runs a shell command via sh -c and returns
// a formatted string with exit_code, stdout, and stderr.
func ExecuteShellCommand(ctx context.Context, args map[string]interface{}) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command must be a string")
	}

	cmd := exec.CommandContext(ctx, "sh", "-c", command)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return "", fmt.Errorf("failed to execute command: %w", err)
		}
	}

	return fmt.Sprintf("exit_code: %d\nstdout:\n%s\nstderr:\n%s", exitCode, stdout.String(), stderr.String()), nil
}
