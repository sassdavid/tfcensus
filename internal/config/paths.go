package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/adrg/xdg"
)

const (
	AppName         = "tfcensus"
	UserFileName    = "config.yaml"
	ProjectFileName = ".tfcensus.yaml"
)

const goosWindows = "windows"

// LookupFunc reads one environment variable.
type LookupFunc func(key string) (string, bool)

// UserConfigPath returns $XDG_CONFIG_HOME/tfcensus/config.yaml. macOS uses ~/.config too,
// not ~/Library.
func UserConfigPath(lookup LookupFunc, goos string) (string, error) {
	dir, err := baseDir(lookup, goos, "XDG_CONFIG_HOME", ".config", xdg.ConfigHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppName, UserFileName), nil
}

// CacheDirPath returns $XDG_CACHE_HOME/tfcensus.
func CacheDirPath(lookup LookupFunc, goos string) (string, error) {
	dir, err := baseDir(lookup, goos, "XDG_CACHE_HOME", ".cache", xdg.CacheHome)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, AppName), nil
}

func baseDir(lookup LookupFunc, goos, envVar, homeRel, windowsDefault string) (string, error) {
	// The XDG spec says relative paths are invalid and must be ignored.
	if d, ok := lookup(envVar); ok && filepath.IsAbs(d) {
		return d, nil
	}
	if goos == goosWindows {
		return windowsDefault, nil
	}
	home, ok := lookup("HOME")
	if !ok || home == "" {
		home = xdg.Home
	}
	if home == "" {
		return "", fmt.Errorf("cannot determine home directory: set HOME or %s", envVar)
	}
	return filepath.Join(home, homeRel), nil
}

// FindProjectFile walks up from dir to the git root, or to stopDir when that comes first,
// looking for .tfcensus.yaml. Outside a git repository and stopDir it reads only dir. It returns
// "" when there is none.
func FindProjectFile(dir, stopDir string) (string, error) {
	dir = filepath.Clean(dir)
	if stopDir != "" {
		stopDir = filepath.Clean(stopDir)
	}
	start, found := dir, ""
	for {
		if found == "" {
			candidate := filepath.Join(dir, ProjectFileName)
			ok, err := isFile(candidate)
			if err != nil {
				return "", err
			}
			if ok && dir == start {
				return candidate, nil
			}
			if ok {
				found = candidate
			}
		}
		if dir == stopDir {
			return found, nil
		}
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return found, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			// No git root above dir: a file in a parent might belong to an unrelated tree,
			// such as the home directory.
			return "", nil
		}
		dir = parent
	}
}

func isFile(path string) (bool, error) {
	fi, err := os.Stat(path)
	switch {
	case err == nil:
		return fi.Mode().IsRegular(), nil
	case errors.Is(err, os.ErrNotExist):
		return false, nil
	default:
		return false, fmt.Errorf("stat %s: %w", path, err)
	}
}
