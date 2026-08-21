//go:build linux

package codexprocess

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
)

func configureBackgroundCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func processPlatformSupported() bool { return true }

func currentProcessIdentity(pid int) (processIdentity, bool, error) {
	stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if os.IsNotExist(err) {
		return processIdentity{}, false, nil
	}
	if err != nil {
		return processIdentity{}, false, err
	}
	closeParen := strings.LastIndexByte(string(stat), ')')
	if closeParen < 0 || closeParen+2 >= len(stat) {
		return processIdentity{}, false, fmt.Errorf("malformed /proc stat")
	}
	fields := strings.Fields(string(stat[closeParen+2:]))
	if len(fields) <= 19 {
		return processIdentity{}, false, fmt.Errorf("incomplete /proc stat")
	}
	startTicks := fields[19]
	groupID, err := strconv.Atoi(fields[2])
	if err != nil || groupID <= 0 {
		return processIdentity{}, false, fmt.Errorf("invalid process group ID")
	}
	if _, err := strconv.ParseUint(startTicks, 10, 64); err != nil {
		return processIdentity{}, false, fmt.Errorf("invalid process start ticks: %w", err)
	}
	bootID, err := os.ReadFile("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return processIdentity{}, false, err
	}
	executable, err := os.Readlink(fmt.Sprintf("/proc/%d/exe", pid))
	if os.IsNotExist(err) {
		return processIdentity{}, false, nil
	}
	if err != nil {
		return processIdentity{}, false, err
	}
	return processIdentity{Fingerprint: strings.TrimSpace(string(bootID)) + ":" + startTicks, Executable: executable, GroupID: groupID}, true, nil
}

func terminateProcessTree(_ context.Context, ref processRef) error {
	status, err := inspectProcessRef(ref)
	if err != nil || status == processGone {
		return err
	}
	if status == processMismatched {
		return fmt.Errorf("PID %d identity changed before termination", ref.PID)
	}
	groupID := ref.GroupID
	if groupID <= 0 {
		groupID = ref.PID
	}
	if err := syscall.Kill(-groupID, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return fmt.Errorf("terminate process group %d: %w", groupID, err)
	}
	return nil
}

func terminateUnverifiedProcess(pid int) error {
	if err := syscall.Kill(-pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
		return err
	}
	return nil
}
