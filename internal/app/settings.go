package app

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Settings remembers the choices users made in interactive prompts so the
// next double-click run starts from the same values. Zero fields mean
// "no remembered value" and prompts fall back to their built-in defaults.
type Settings struct {
	PDFQualities []int `json:"pdfQualities,omitempty"`
	SVGFps       int   `json:"svgFps,omitempty"`
}

// SettingsPath returns the settings file location next to the executable.
// The toolbox already expects its input/output directories to sit next to
// the binary, so the settings file follows the same portable layout.
func SettingsPath() (string, error) {
	base, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "toolbox.settings.json"), nil
}

// LoadSettings reads the settings file. A missing or unreadable file is not
// an error: preferences simply start from the built-in defaults.
func LoadSettings() Settings {
	path, err := SettingsPath()
	if err != nil {
		return Settings{}
	}
	settings, _ := loadSettingsFile(path)
	return settings
}

// SaveSettings writes the settings file atomically. Failures are reported
// but never block the tool run that produced the preference.
func SaveSettings(settings Settings) error {
	path, err := SettingsPath()
	if err != nil {
		return err
	}
	return saveSettingsFile(path, settings)
}

func loadSettingsFile(path string) (Settings, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Settings{}, err
	}
	var settings Settings
	if err := json.Unmarshal(data, &settings); err != nil {
		return Settings{}, fmt.Errorf("解析设置文件 %s: %w", path, err)
	}
	return settings, nil
}

func saveSettingsFile(path string, settings Settings) error {
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("编码设置: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建设置目录: %w", err)
	}
	tempFile, err := os.CreateTemp(filepath.Dir(path), ".toolbox-settings-*.json")
	if err != nil {
		return fmt.Errorf("创建临时设置文件: %w", err)
	}
	tempPath := tempFile.Name()
	defer os.Remove(tempPath)

	if _, err := tempFile.Write(append(data, '\n')); err != nil {
		tempFile.Close()
		return fmt.Errorf("写入设置文件: %w", err)
	}
	if err := tempFile.Close(); err != nil {
		return fmt.Errorf("关闭设置文件: %w", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("保存设置文件: %w", err)
	}
	return nil
}
