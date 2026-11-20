//go:build !windows

package main

import (
	"os"
	"os/exec"
	"syscall"
)

// extensionShellCommand 用 POSIX shell 跑扩展的启动命令（Linux/macOS）。
func extensionShellCommand(run string) *exec.Cmd {
	return exec.Command("sh", "-c", run)
}

// extensionSysProcAttr 让扩展进程进独立进程组：go run 会再起一个子进程，
// 停的时候要连同它一起停。
func extensionSysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// terminateExtension 给整个进程组发信号。force=false 先 SIGTERM，force=true 再 SIGKILL。
func terminateExtension(cmd *exec.Cmd, force bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	_ = syscall.Kill(-cmd.Process.Pid, sig)
}

// extensionInterruptSignals 是 `extension dev` 等前台命令的退出信号。
func extensionInterruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
