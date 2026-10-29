package main

import (
	"embed"
	"encoding/json"
	"errors"
	"log"
	"os"
	"path/filepath"

	"tuiner/tuning"
)

//go:embed config/*.json
var configFS embed.FS

type SettingsSelections struct {
	Tuning         string  `json:"selected_tuning"`
	AudioInput     string  `json:"audio_input,omitempty"`
	BufferLength   int     `json:"buffer_length"`
	SampleRate     int     `json:"sample_rate"`
	MinFrequency   int     `json:"min_frequency"`
	MaxFrequency   int     `json:"max_frequency"`
	AmplTreshold   float32 `json:"amplitude_treshold"`
	YinMinTreshold float32 `json:"yin_min_treshold"`
	YinMaxTreshold float32 `json:"yin_max_treshold"`
	HistorySize    int     `json:"history_size"`
}

type SettingsData struct {
	Tunings []tuning.Tuning `json:"tunings"`
}

func LoadOrWriteConfigFile(name string) ([]byte, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, err
	}
	appDir := filepath.Join(configDir, "tuiner")
	if err := os.MkdirAll(appDir, 0700); err != nil {
		return nil, err
	}
	path := filepath.Join(appDir, name)
	data, err := os.ReadFile(path)
	if err == nil {
		return data, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	data, err = configFS.ReadFile("config/" + name)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		return nil, err
	}
	return data, nil
}

func LoadSettingsData() SettingsData {
	var user SettingsData
	if raw, err := LoadOrWriteConfigFile("custom_options.json"); err != nil {
		log.Printf("tuiner: load custom tunings: %v", err)
	} else if err := json.Unmarshal(raw, &user); err != nil {
		log.Printf("tuiner: parse custom tunings: %v", err)
	}

	var defaults SettingsData
	raw, err := configFS.ReadFile("config/default_options.json")
	if err != nil {
		log.Printf("tuiner: load built-in tunings: %v", err)
		return user
	}
	if err := json.Unmarshal(raw, &defaults); err != nil {
		log.Printf("tuiner: parse built-in tunings: %v", err)
		return user
	}

	result := SettingsData{Tunings: append([]tuning.Tuning(nil), user.Tunings...)}
	seen := make(map[string]bool, len(result.Tunings))
	for _, item := range result.Tunings {
		seen[item.Name] = true
	}
	for _, item := range defaults.Tunings {
		if !seen[item.Name] {
			result.Tunings = append(result.Tunings, item)
		}
	}
	return result
}

func LoadSettingsSelections(name string, data SettingsData) SettingsSelections {
	raw, err := LoadOrWriteConfigFile(name)
	if err != nil {
		log.Printf("tuiner: load settings: %v", err)
		return SettingsSelections{}
	}
	var settings SettingsSelections
	if err := json.Unmarshal(raw, &settings); err == nil {
		return settings
	}

	// Migrate the original format where selected_tuning was an array index.
	var legacy struct {
		Tuning         int     `json:"selected_tuning"`
		AudioInput     string  `json:"audio_input,omitempty"`
		BufferLength   int     `json:"buffer_length"`
		SampleRate     int     `json:"sample_rate"`
		MinFrequency   int     `json:"min_frequency"`
		MaxFrequency   int     `json:"max_frequency"`
		AmplTreshold   float32 `json:"amplitude_treshold"`
		YinMinTreshold float32 `json:"yin_min_treshold"`
		YinMaxTreshold float32 `json:"yin_max_treshold"`
		HistorySize    int     `json:"history_size"`
	}
	if err := json.Unmarshal(raw, &legacy); err != nil {
		log.Printf("tuiner: parse settings: %v", err)
		return SettingsSelections{}
	}
	settings = SettingsSelections{
		AudioInput: legacy.AudioInput, BufferLength: legacy.BufferLength, SampleRate: legacy.SampleRate,
		MinFrequency: legacy.MinFrequency, MaxFrequency: legacy.MaxFrequency,
		AmplTreshold: legacy.AmplTreshold, YinMinTreshold: legacy.YinMinTreshold,
		YinMaxTreshold: legacy.YinMaxTreshold, HistorySize: legacy.HistorySize,
	}
	if legacy.Tuning >= 0 && legacy.Tuning < len(data.Tunings) {
		settings.Tuning = data.Tunings[legacy.Tuning].Name
	}
	StoreSettings(settings, name)
	return settings
}

func StoreSettings(settings SettingsSelections, name string) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		log.Printf("tuiner: locate config: %v", err)
		return
	}
	appDir := filepath.Join(configDir, "tuiner")
	if err := os.MkdirAll(appDir, 0700); err != nil {
		log.Printf("tuiner: create config directory: %v", err)
		return
	}
	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		log.Printf("tuiner: encode settings: %v", err)
		return
	}
	if err := os.WriteFile(filepath.Join(appDir, name), data, 0600); err != nil {
		log.Printf("tuiner: save settings: %v", err)
	}
}
