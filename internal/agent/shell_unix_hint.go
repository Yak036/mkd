//go:build !windows

package agent

// shellHint describe el intérprete usado por run_command (ver shell_unix.go).
func shellHint() string { return "/bin/sh" }
