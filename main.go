package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func main() {
	sseMode := flag.Bool("sse", false, "Run in SSE mode instead of stdio mode")
	flag.Parse()

	mcpServer := server.NewMCPServer(
		"python-executor",
		"1.0.0",
	)

	pythonTool := mcp.NewTool(
		"execute-python",
		mcp.WithDescription(
			"Execute Python code in an isolated environment. Playwright and headless browser are available for web scraping. Use this tool when you need real-time information, only output printed to stdout or stderr is returned so ALWAYS use print statements! Please note all code is run in an ephemeral container so modules and code do not persist.",
		),
		mcp.WithString(
			"code",
			mcp.Description("The Python code to execute"),
			mcp.Required(),
		),
		mcp.WithString(
			"modules",
			mcp.Description("Comma-separated list of Python modules to install. If your code requires external modules, we will install the modules before trying to run the python code."),
		))

	mcpServer.AddTool(pythonTool, handlePythonExecution)

	if *sseMode {
		sseServer := server.NewSSEServer(mcpServer, server.WithBaseURL("http://localhost:8080"))
		log.Printf("Starting SSE server on http://localhost:8080")
		if err := sseServer.Start(":8080"); err != nil {
			log.Fatalf("Failed to start SSE server: %v", err)
		}
	} else {
		if err := server.ServeStdio(mcpServer); err != nil {
			log.Fatalf("Failed to start stdio server: %v", err)
		}
	}
}

func handlePythonExecution(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	args, ok := request.Params.Arguments.(map[string]any)
	if !ok {
		return mcp.NewToolResultError("Invalid arguments"), nil
	}

	code, ok := args["code"].(string)
	if !ok {
		return mcp.NewToolResultError("Missing or invalid code parameter"), nil
	}

	var modules []string
	if modulesStr, ok := args["modules"].(string); ok && modulesStr != "" {
		modules = strings.Split(modulesStr, ",")
	}

	tmpDir, err := os.MkdirTemp("", "python_repl")
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to create temporary directory: %v", err)), nil
	}
	defer os.RemoveAll(tmpDir)

	err = os.WriteFile(path.Join(tmpDir, "script.py"), []byte(code), 0644)
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("Failed to write script to temporary directory: %v", err)), nil
	}

	// Run the Python code in a docker container
	cmdArgs := []string{
		"run",
		"--rm",
		"-v",
		fmt.Sprintf("%s:/app", tmpDir),
		"mcr.microsoft.com/playwright/python:v1.49.1-noble",
	}
	shArgs := []string{}

	// If modules are provided, install them
	if len(modules) > 0 {
		shArgs = append(shArgs, "python", "-m", "pip", "install", "--quiet")
		shArgs = append(shArgs, modules...)
		shArgs = append(shArgs, "&&")
	}

	shArgs = append(shArgs, "python", path.Join("app", "script.py"))
	cmdArgs = append(cmdArgs, "sh", "-c", strings.Join(shArgs, " "))

	cmd := exec.Command("docker", cmdArgs...)
	out, err := cmd.Output()
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			return mcp.NewToolResultError(fmt.Sprintf("Python execution failed with code %d: %s", exitError.ExitCode(), string(exitError.Stderr))), nil
		}
		return mcp.NewToolResultError(fmt.Sprintf("Python execution failed: %v", err)), nil
	}

	return mcp.NewToolResultText(string(out)), nil
}
