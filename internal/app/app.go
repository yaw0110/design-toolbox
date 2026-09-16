// Package app provides helpers shared by all toolbox tools:
// locating runtime directories next to the executable and
// pausing for interactive double-click launches.
package app

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
)

// BaseDir returns the directory that contains the running executable.
// All input/output directories are resolved relative to it so the
// toolbox keeps working when double-clicked from Finder or Explorer.
func BaseDir() (string, error) {
	executablePath, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("定位程序文件: %w", err)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(executablePath); resolveErr == nil {
		executablePath = resolvedPath
	}
	return filepath.Dir(executablePath), nil
}

// ToolInputDir returns the input directory for a tool, e.g. input/pdf.
func ToolInputDir(tool string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "input", tool), nil
}

// ToolOutputDir returns the output directory for a tool, e.g. output/pdf.
func ToolOutputDir(tool string) (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "output", tool), nil
}

// PauseIfInteractive waits for the user to press Enter, but only when
// stdin is an interactive terminal. Batch pipelines and double-click
// launches with redirected stdin are not blocked.
func PauseIfInteractive(message string) {
	info, err := os.Stdin.Stat()
	if err != nil || info.Mode()&os.ModeCharDevice == 0 {
		return
	}

	fmt.Println(message)
	_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
}
