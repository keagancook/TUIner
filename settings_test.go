package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsUseTuinerConfigDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	if _, err := LoadOrWriteConfigFile("custom_options.json"); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "tuiner", "custom_options.json")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("config permissions = %o, want 600", info.Mode().Perm())
	}
	StoreSettings(SettingsSelections{Tuning: "Standard"}, "selections.json")
	if _, err := os.Stat(filepath.Join(root, "tuiner", "selections.json")); err != nil {
		t.Fatal(err)
	}
}

func TestLogUsesTuinerCacheDirectory(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", root)
	want := filepath.Join(root, "tuiner", "debug.log")
	if got := logFilePath(); got != want {
		t.Fatalf("log path = %q, want %q", got, want)
	}
	info, err := os.Stat(filepath.Dir(want))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0700 {
		t.Fatalf("log directory permissions = %o, want 700", info.Mode().Perm())
	}
}
