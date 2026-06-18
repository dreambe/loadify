// Command loadify-mcp is a Model Context Protocol server (stdio) that lets an
// agent create and run load tests against a loadify apisrv. Configure it with:
//
//	LOADIFY_API       apisrv base URL (default http://localhost:8080)
//	LOADIFY_TOKEN     a bearer token, or
//	LOADIFY_EMAIL + LOADIFY_PASSWORD  to log in for one
//
// Register it with any MCP client, e.g. Claude:
//
//	{ "mcpServers": { "loadify": { "command": "loadify-mcp",
//	  "env": { "LOADIFY_API": "http://localhost:8080", "LOADIFY_TOKEN": "..." } } } }
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/dreambe/loadify/internal/apiclient"
	"github.com/dreambe/loadify/internal/mcp"
)

func main() {
	base := envOr("LOADIFY_API", "http://localhost:8080")
	client := apiclient.New(base, os.Getenv("LOADIFY_TOKEN"))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if client.Token == "" {
		email, pw := os.Getenv("LOADIFY_EMAIL"), os.Getenv("LOADIFY_PASSWORD")
		if email != "" && pw != "" {
			if _, err := client.Login(ctx, email, pw); err != nil {
				fmt.Fprintln(os.Stderr, "loadify-mcp: login failed:", err)
				os.Exit(1)
			}
		}
	}

	srv := mcp.NewServer(client)
	if err := srv.Serve(ctx, os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "loadify-mcp:", err)
		os.Exit(1)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
