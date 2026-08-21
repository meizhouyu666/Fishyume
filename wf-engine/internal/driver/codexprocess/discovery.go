package codexprocess

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

type discoveredExecutable struct {
	Path   string
	SHA256 string
}

type discoveryCache struct {
	Path             string
	Size             int64
	ModifiedUnixNano int64
	Executable       discoveredExecutable
}

func (b *Backend) discoverExecutable() (discoveredExecutable, error) {
	override := strings.TrimSpace(b.config.Executable)
	if override == "" {
		override = strings.TrimSpace(os.Getenv("FISHYUME_CODEX_PATH"))
	}
	var path string
	var err error
	if override != "" {
		path, err = exec.LookPath(override)
	} else {
		path, err = exec.LookPath("codex")
	}
	if err != nil {
		return discoveredExecutable{}, fmt.Errorf("Codex CLI was not found; install @openai/codex or set FISHYUME_CODEX_PATH: %w", err)
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return discoveredExecutable{}, err
	}
	path = filepath.Clean(path)
	if shimLooksNonNative(path) {
		if native := npmNativeCodex(path); native != "" {
			path = native
		} else {
			return discoveredExecutable{}, fmt.Errorf("Codex CLI path %s is a script shim; set FISHYUME_CODEX_PATH to the native Codex executable", path)
		}
	}
	if resolved, resolveErr := filepath.EvalSymlinks(path); resolveErr == nil {
		path = filepath.Clean(resolved)
	}
	info, err := os.Stat(path)
	if err != nil {
		return discoveredExecutable{}, fmt.Errorf("inspect Codex CLI %s: %w", path, err)
	}
	if info.IsDir() {
		return discoveredExecutable{}, fmt.Errorf("Codex CLI path %s is a directory", path)
	}
	b.discoveryMu.Lock()
	if cached := b.discovery; cached != nil && cached.Path == path && cached.Size == info.Size() && cached.ModifiedUnixNano == info.ModTime().UnixNano() {
		result := cached.Executable
		b.discoveryMu.Unlock()
		return result, nil
	}
	b.discoveryMu.Unlock()
	hash, err := executableSHA256(path)
	if err != nil {
		return discoveredExecutable{}, fmt.Errorf("identify Codex CLI executable: %w", err)
	}
	result := discoveredExecutable{Path: path, SHA256: hash}
	b.discoveryMu.Lock()
	b.discovery = &discoveryCache{Path: path, Size: info.Size(), ModifiedUnixNano: info.ModTime().UnixNano(), Executable: result}
	b.discoveryMu.Unlock()
	return result, nil
}

func (b *Backend) supervisorExecutable() (string, error) {
	path := strings.TrimSpace(b.config.SupervisorExecutable)
	if path == "" {
		var err error
		path, err = os.Executable()
		if err != nil {
			return "", fmt.Errorf("resolve Fishyume Engine executable: %w", err)
		}
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	if info, err := os.Stat(absolute); err != nil || info.IsDir() {
		return "", fmt.Errorf("Direct supervisor executable is unavailable at %s", absolute)
	}
	return filepath.Clean(absolute), nil
}

func shimLooksNonNative(path string) bool {
	if runtime.GOOS == "windows" {
		return !strings.EqualFold(filepath.Ext(path), ".exe")
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	prefix := make([]byte, 2)
	_, _ = file.Read(prefix)
	return string(prefix) == "#!"
}

func npmNativeCodex(shimPath string) string {
	packageName, triple, binary := "", "", "codex"
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "windows/amd64":
		packageName, triple, binary = "codex-win32-x64", "x86_64-pc-windows-msvc", "codex.exe"
	case "windows/arm64":
		packageName, triple, binary = "codex-win32-arm64", "aarch64-pc-windows-msvc", "codex.exe"
	case "linux/amd64":
		packageName, triple = "codex-linux-x64", "x86_64-unknown-linux-musl"
	case "linux/arm64":
		packageName, triple = "codex-linux-arm64", "aarch64-unknown-linux-musl"
	case "darwin/amd64":
		packageName, triple = "codex-darwin-x64", "x86_64-apple-darwin"
	case "darwin/arm64":
		packageName, triple = "codex-darwin-arm64", "aarch64-apple-darwin"
	default:
		return ""
	}
	base := filepath.Dir(shimPath)
	candidates := []string{
		filepath.Join(base, "node_modules", "@openai", "codex", "node_modules", "@openai", packageName, "vendor", triple, "bin", binary),
		filepath.Join(base, "node_modules", "@openai", packageName, "vendor", triple, "bin", binary),
		filepath.Join(filepath.Dir(base), "lib", "node_modules", "@openai", "codex", "node_modules", "@openai", packageName, "vendor", triple, "bin", binary),
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			absolute, _ := filepath.Abs(candidate)
			return filepath.Clean(absolute)
		}
	}
	return ""
}
