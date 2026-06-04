//go:build !windows

package mcp

import "syscall"

// detachAttrs detaches the spawned daemon from the MCP process on Unix: Setsid
// starts a new session so the daemon survives the MCP process exiting and is not
// killed by signals sent to the MCP process group (D2: detached background).
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
