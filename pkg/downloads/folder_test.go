package downloads

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestResolveOSDownloadsDir(t *testing.T) {
	fakeHome := "/home/testuser"
	fakeWinHome := "C:\\Users\\testuser"

	tests := []struct {
		name     string
		goos     string
		env      map[string]string
		home     string
		files    map[string][]byte
		dirs     map[string]bool
		expected string
	}{
		{
			name: "macOS standard ~/Downloads",
			goos: "darwin",
			home: fakeHome,
			dirs: map[string]bool{
				filepath.Join(fakeHome, "Downloads"): true,
			},
			expected: filepath.Join(fakeHome, "Downloads"),
		},
		{
			name: "Windows with USERPROFILE",
			goos: "windows",
			env: map[string]string{
				"USERPROFILE": fakeWinHome,
			},
			home: fakeWinHome,
			dirs: map[string]bool{
				filepath.Join(fakeWinHome, "Downloads"): true,
			},
			expected: filepath.Join(fakeWinHome, "Downloads"),
		},
		{
			name: "Linux with XDG_DOWNLOAD_DIR set directly",
			goos: "linux",
			env: map[string]string{
				"XDG_DOWNLOAD_DIR": "$HOME/MyCustomDownloads",
			},
			home: fakeHome,
			dirs: map[string]bool{
				filepath.Join(fakeHome, "MyCustomDownloads"): true,
			},
			expected: filepath.Join(fakeHome, "MyCustomDownloads"),
		},
		{
			name: "Linux with ~/.config/user-dirs.dirs",
			goos: "linux",
			home: fakeHome,
			files: map[string][]byte{
				filepath.Join(fakeHome, ".config", "user-dirs.dirs"): []byte(
					"# This is a comment\nXDG_DESKTOP_DIR=\"$HOME/Desktop\"\nXDG_DOWNLOAD_DIR=\"$HOME/Incoming\"\n",
				),
			},
			dirs: map[string]bool{
				filepath.Join(fakeHome, "Incoming"): true,
			},
			expected: filepath.Join(fakeHome, "Incoming"),
		},
		{
			name: "Linux fallback to ~/Downloads if no XDG configuration",
			goos: "linux",
			home: fakeHome,
			dirs: map[string]bool{
				filepath.Join(fakeHome, "Downloads"): true,
			},
			expected: filepath.Join(fakeHome, "Downloads"),
		},
		{
			name: "Fallback to home if Downloads dir does not exist",
			goos: "linux",
			home: fakeHome,
			dirs: map[string]bool{
				fakeHome: true,
			},
			expected: fakeHome,
		},
		{
			name:     "Fallback to current dir if no home",
			goos:     "linux",
			home:     "",
			dirs:     map[string]bool{},
			expected: ".",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			getenv := func(key string) string {
				if tc.env != nil {
					return tc.env[key]
				}
				return ""
			}
			homeDirFn := func() (string, error) {
				if tc.home == "" {
					return "", errors.New("no home")
				}
				return tc.home, nil
			}
			readFileFn := func(path string) ([]byte, error) {
				if data, ok := tc.files[path]; ok {
					return data, nil
				}
				return nil, errors.New("file not found")
			}
			isDirFn := func(path string) bool {
				return tc.dirs[path]
			}

			got := resolveOSDownloadsDir(tc.goos, getenv, homeDirFn, readFileFn, isDirFn)
			if got != tc.expected {
				t.Errorf("resolveOSDownloadsDir() = %q, expected %q", got, tc.expected)
			}
		})
	}
}
