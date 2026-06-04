//go:build windows

package mcp

import "syscall"

// Windows process creation flags for a detached daemon. DETACHED_PROCESS gives
// the child no console (it does not share the MCP process's console), and
// CREATE_NEW_PROCESS_GROUP isolates it from Ctrl-C / Ctrl-Break delivered to the
// MCP process group (D2: detached background).
const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detachAttrs detaches the spawned daemon from the MCP process on Windows.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}
