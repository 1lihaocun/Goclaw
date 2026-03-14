package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

const (
	EditablePathOriginRuntime          = "runtime"
	EditablePathOriginWorkingDirectory = "working_directory"
)

type EditablePath struct {
	Path   string
	Origin string
	Exists bool
}

func ResolveEditablePath(cfg Config) (EditablePath, error) {
	if path := strings.TrimSpace(cfg.SourcePath); path != "" {
		absPath, err := filepath.Abs(path)
		if err != nil {
			return EditablePath{}, fmt.Errorf("resolve config path %q: %w", path, err)
		}
		return EditablePath{
			Path:   absPath,
			Origin: EditablePathOriginRuntime,
			Exists: fileExists(absPath),
		}, nil
	}

	workingDir, err := os.Getwd()
	if err != nil {
		return EditablePath{}, fmt.Errorf("resolve working directory: %w", err)
	}
	path := filepath.Join(workingDir, "goclaw.json")
	return EditablePath{
		Path:   path,
		Origin: EditablePathOriginWorkingDirectory,
		Exists: fileExists(path),
	}, nil
}

func DefaultMap() (map[string]any, error) {
	return ConfigToMap(Default())
}

func ConfigToMap(cfg Config) (map[string]any, error) {
	cfg.SourcePath = ""
	data, err := json.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("encode config: %w", err)
	}

	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("decode config map: %w", err)
	}
	return root, nil
}

func LoadEffectiveMap(path string) (map[string]any, error) {
	cfg, err := Load(LoadOptions{Path: path})
	if err != nil {
		return nil, err
	}
	return ConfigToMap(cfg)
}

func LoadFileMap(path string) (map[string]any, bool, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return map[string]any{}, false, nil
	}

	data, err := os.ReadFile(trimmedPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]any{}, false, nil
		}
		return nil, false, fmt.Errorf("read config file %q: %w", trimmedPath, err)
	}
	if err := validateRawConfigJSON(data); err != nil {
		return nil, false, fmt.Errorf("validate config file %q: %w", trimmedPath, err)
	}

	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()

	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		return nil, false, fmt.Errorf("decode config map %q: %w", trimmedPath, err)
	}
	return root, true, nil
}

func SaveFileMap(path string, root map[string]any) error {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return errors.New("config file path is required")
	}

	normalized, err := NormalizeFileMap(root)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(trimmedPath), 0o755); err != nil {
		return fmt.Errorf("create config dir %q: %w", filepath.Dir(trimmedPath), err)
	}
	if err := os.WriteFile(trimmedPath, normalized, 0o644); err != nil {
		return fmt.Errorf("write config file %q: %w", trimmedPath, err)
	}
	return nil
}

func NormalizeFileMap(root map[string]any) ([]byte, error) {
	if root == nil {
		root = map[string]any{}
	}

	data, err := json.Marshal(root)
	if err != nil {
		return nil, fmt.Errorf("encode config map: %w", err)
	}
	if err := validateRawConfigJSON(data); err != nil {
		return nil, err
	}

	pretty, err := json.MarshalIndent(root, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("format config map: %w", err)
	}
	return append(pretty, '\n'), nil
}

func validateRawConfigJSON(data []byte) error {
	var cfg Config
	if err := decodeConfigJSON(data, &cfg); err != nil {
		return err
	}
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func decodeConfigJSON(data []byte, target *Config) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	return errors.New("unexpected extra JSON content")
}
