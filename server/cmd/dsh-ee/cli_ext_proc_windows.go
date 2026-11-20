//go:build windows

package main

import (
	"os"
	"os/exec"
	"strconv"
	"syscall"
)

// extensionShellCommand 在 Windows 上用 cmd.exe 跑扩展的启动命令。
// 与 POSIX 一样由用户在 --run 里给出命令；Windows 没有 sh，接受 cmd 语法。
func extensionShellCommand(run string) *exec.Cmd {
	return exec.Command("cmd.exe", "/c", run)
}

// extensionSysProcAttr 在 Windows 上没有进程组可设（taskkill /T 会处理子进程树）。
func extensionSysProcAttr() *syscall.SysProcAttr {
	return nil
}

// terminateExtension 用 taskkill /T 停掉扩展及其子进程；force 时加 /F。
func terminateExtension(cmd *exec.Cmd, force bool) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	args := []string{"/T", "/PID", strconv.Itoa(cmd.Process.Pid)}
	if force {
		args = append([]string{"/F"}, args...)
	}
	_ = exec.Command("taskkill", args...).Run()
	// taskkill 失败（比如进程已退出）时兜底直接 Kill 本进程。
	_ = cmd.Process.Kill()
}

// extensionInterruptSignals 在 Windows 上只有 Ctrl+C。
func extensionInterruptSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
