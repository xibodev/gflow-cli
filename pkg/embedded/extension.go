package embedded

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

//go:embed files/*
var extensionFS embed.FS

// GetDefaultExtensionDir returns the standard path where the extension is unpacked (~/.gflow/extension).
func GetDefaultExtensionDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".gflow", "extension"), nil
}

// ExtractExtension extracts the embedded Chrome extension to targetDir.
func ExtractExtension(targetDir string) (string, error) {
	if targetDir == "" {
		var err error
		targetDir, err = GetDefaultExtensionDir()
		if err != nil {
			return "", fmt.Errorf("could not resolve home directory: %w", err)
		}
	}

	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return "", fmt.Errorf("could not create directory %s: %w", targetDir, err)
	}

	entries, err := extensionFS.ReadDir("files")
	if err != nil {
		return "", fmt.Errorf("could not read embedded extension files: %w", err)
	}

	for _, entry := range entries {
		data, err := extensionFS.ReadFile("files/" + entry.Name())
		if err != nil {
			return "", fmt.Errorf("could not read %s from embed: %w", entry.Name(), err)
		}

		destPath := filepath.Join(targetDir, entry.Name())
		if err := os.WriteFile(destPath, data, 0644); err != nil {
			return "", fmt.Errorf("could not write %s: %w", destPath, err)
		}
	}

	return targetDir, nil
}

// GetExtensionFS returns the embedded filesystem.
func GetExtensionFS() fs.FS {
	sub, _ := fs.Sub(extensionFS, "files")
	return sub
}
