package app

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestSettingsFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toolbox.settings.json")
	settings := Settings{PDFQualities: []int{40, 50, 60}, SVGFps: 24}

	if err := saveSettingsFile(path, settings); err != nil {
		t.Fatalf("save settings: %v", err)
	}
	loaded, err := loadSettingsFile(path)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if !reflect.DeepEqual(loaded, settings) {
		t.Fatalf("loaded settings = %+v, want %+v", loaded, settings)
	}
}

func TestLoadSettingsFileMissing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.json")
	if _, err := loadSettingsFile(path); !os.IsNotExist(err) {
		t.Fatalf("missing file error = %v, want not-exist", err)
	}
}

func TestLoadSettingsFileCorrupt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "toolbox.settings.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt settings: %v", err)
	}
	if _, err := loadSettingsFile(path); err == nil {
		t.Fatal("corrupt settings file was accepted")
	}
}
