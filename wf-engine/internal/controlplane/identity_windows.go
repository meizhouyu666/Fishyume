//go:build windows

package controlplane

import (
	"fmt"

	"golang.org/x/sys/windows"
)

func currentUserIdentity() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", fmt.Errorf("open current process token: %w", err)
	}
	defer token.Close()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", fmt.Errorf("read current user SID: %w", err)
	}
	return user.User.Sid.String(), nil
}

func secureStateDirectory(string) error { return nil }
