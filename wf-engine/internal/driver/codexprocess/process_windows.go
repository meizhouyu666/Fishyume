//go:build windows

package codexprocess

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/windows"
)

func configureBackgroundCommand(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_PROCESS_GROUP | windows.CREATE_NO_WINDOW}
}

func processPlatformSupported() bool { return true }

func currentProcessIdentity(pid int) (processIdentity, bool, error) {
	handle, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION|windows.SYNCHRONIZE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return processIdentity{}, false, nil
	}
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// A PID that becomes inaccessible cannot be trusted as the persisted
		// process. Report an intentionally non-matching identity so callers
		// never observe or terminate it by PID alone.
		return processIdentity{Fingerprint: "inaccessible"}, true, nil
	}
	if err != nil {
		return processIdentity{}, false, err
	}
	defer windows.CloseHandle(handle)
	result, err := windows.WaitForSingleObject(handle, 0)
	if err != nil {
		return inaccessibleProcessIdentity(err)
	}
	if result == windows.WAIT_OBJECT_0 {
		return processIdentity{}, false, nil
	}
	var creation, exit, kernel, user windows.Filetime
	if err := windows.GetProcessTimes(handle, &creation, &exit, &kernel, &user); err != nil {
		return inaccessibleProcessIdentity(err)
	}
	buffer := make([]uint16, windows.MAX_PATH*4)
	size := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(handle, 0, &buffer[0], &size); err != nil {
		return inaccessibleProcessIdentity(err)
	}
	return processIdentity{Fingerprint: fmt.Sprintf("%d", creation.Nanoseconds()), Executable: filepath.Clean(windows.UTF16ToString(buffer[:size]))}, true, nil
}

func inaccessibleProcessIdentity(err error) (processIdentity, bool, error) {
	if errors.Is(err, windows.ERROR_ACCESS_DENIED) {
		// The handle may become inaccessible while a process is exiting. Keep
		// treating the PID as occupied but intentionally non-matching so it is
		// never observed or terminated using stale identity evidence.
		return processIdentity{Fingerprint: "inaccessible"}, true, nil
	}
	return processIdentity{}, false, err
}

func terminateProcessTree(ctx context.Context, ref processRef) error {
	status, err := inspectProcessRef(ref)
	if err != nil || status == processGone {
		return err
	}
	if status == processMismatched {
		return fmt.Errorf("PID %d identity changed before termination", ref.PID)
	}
	command := exec.CommandContext(ctx, "taskkill.exe", "/PID", fmt.Sprintf("%d", ref.PID), "/T", "/F")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	output, err := command.CombinedOutput()
	if err != nil {
		status, inspectErr := inspectProcessRef(ref)
		if inspectErr == nil && status != processMatched {
			return nil
		}
		return fmt.Errorf("terminate process tree %d: %w: %s", ref.PID, err, output)
	}
	return nil
}

func terminateUnverifiedProcess(pid int) error {
	handle, err := windows.OpenProcess(windows.PROCESS_TERMINATE, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(handle)
	return windows.TerminateProcess(handle, 125)
}
