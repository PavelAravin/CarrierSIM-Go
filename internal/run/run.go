package run

import (
	"encoding/json"
	"os"
	"path/filepath"
)

func SaveJSON(path string, value interface{}) error {
	tmp := path + ".tmp"
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write(raw); err != nil {
		f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func ReadJSON(path string, out interface{}) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, out)
}

func EnsureDir(path string, mode os.FileMode) error {
	return os.MkdirAll(path, mode)
}

func NewRunDir(runs string, name string) (string, error) {
	dir := filepath.Join(runs, name)
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
