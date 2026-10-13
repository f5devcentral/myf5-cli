package downloads

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// DefaultDownloadsDir returns the operating system's canonical downloads directory.
//
// Resolution strategy:
//   - Linux / BSD: Respects XDG User Directories specifications ($XDG_DOWNLOAD_DIR or
//     ~/.config/user-dirs.dirs), falling back to ~/Downloads, user home, or current directory.
//   - Windows: Checks %USERPROFILE%\Downloads and ~/Downloads, falling back to user home or current directory.
//   - macOS: Standard ~/Downloads directory, falling back to user home or current directory.
func DefaultDownloadsDir() string {
	return resolveOSDownloadsDir(
		runtime.GOOS,
		os.Getenv,
		os.UserHomeDir,
		os.ReadFile,
		func(path string) bool {
			fi, err := os.Stat(path)
			return err == nil && fi.IsDir()
		},
	)
}

// resolveOSDownloadsDir is the internal resolution logic parameterized for deterministic cross-platform unit testing.
func resolveOSDownloadsDir(
	goos string,
	getenv func(string) string,
	homeDirFn func() (string, error),
	readFileFn func(string) ([]byte, error),
	isDirFn func(string) bool,
) string {
	home, _ := homeDirFn()

	switch goos {
	case "windows":
		// Windows: Check %USERPROFILE%\Downloads first
		if up := getenv("USERPROFILE"); up != "" {
			cand := filepath.Join(up, "Downloads")
			if isDirFn(cand) {
				return cand
			}
		}
		if home != "" {
			cand := filepath.Join(home, "Downloads")
			if isDirFn(cand) {
				return cand
			}
			return home
		}
		return "."

	case "linux", "freebsd", "openbsd", "netbsd":
		// 1. Direct environment variable: $XDG_DOWNLOAD_DIR
		if xdgDl := getenv("XDG_DOWNLOAD_DIR"); xdgDl != "" {
			cand := expandHome(xdgDl, home)
			if isDirFn(cand) {
				return cand
			}
		}

		// 2. XDG user-dirs configuration file (~/.config/user-dirs.dirs)
		configHome := getenv("XDG_CONFIG_HOME")
		if configHome == "" && home != "" {
			configHome = filepath.Join(home, ".config")
		}
		if configHome != "" {
			confFile := filepath.Join(configHome, "user-dirs.dirs")
			if data, err := readFileFn(confFile); err == nil {
				if parsed := parseXDGUserDirs(data, "XDG_DOWNLOAD_DIR", home); parsed != "" {
					if isDirFn(parsed) {
						return parsed
					}
				}
			}
		}

		// 3. Fallback to standard ~/Downloads
		if home != "" {
			cand := filepath.Join(home, "Downloads")
			if isDirFn(cand) {
				return cand
			}
			return home
		}
		return "."

	default: // "darwin" and other operating systems
		if home != "" {
			cand := filepath.Join(home, "Downloads")
			if isDirFn(cand) {
				return cand
			}
			return home
		}
		return "."
	}
}

// expandHome replaces leading "$HOME" or "${HOME}" with the evaluated user home path.
func expandHome(pathStr, home string) string {
	if strings.HasPrefix(pathStr, "$HOME") {
		return filepath.Join(home, strings.TrimPrefix(pathStr, "$HOME"))
	}
	if strings.HasPrefix(pathStr, "${HOME}") {
		return filepath.Join(home, strings.TrimPrefix(pathStr, "${HOME}"))
	}
	return pathStr
}

// parseXDGUserDirs extracts the value of a key from an XDG user-dirs shell-syntax config.
func parseXDGUserDirs(data []byte, key, home string) string {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	prefix := key + "="
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, prefix) {
			val := strings.TrimPrefix(line, prefix)
			val = strings.Trim(val, "\"")
			val = strings.Trim(val, "'")
			return expandHome(val, home)
		}
	}
	return ""
}
