package library

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type Files struct{ Root string }

// blobPath takes a validated SHA-256 hex hash and distributes primary files
// over its first four character pairs.
// Cache bodies keep their existing layout and cleanup policy.
func blobPath(kind, hash string) (string, error) {
	var extension string
	switch kind {
	case "texts":
		extension = ".txt"
	case "digests":
		extension = ".md"
	case "cache/http":
		return filepath.Join(kind, hash+".body"), nil
	default:
		return "", errors.New("invalid blob kind")
	}
	return filepath.Join(kind, hash[:2], hash[2:4], hash[4:6], hash[6:8], hash+extension), nil
}

func (f Files) Put(kind string, data []byte) (string, error) {
	hash := fmt.Sprintf("%x", sha256.Sum256(data))
	relative, err := blobPath(kind, hash)
	if err != nil {
		return "", err
	}
	path := filepath.Join(f.Root, relative)
	if _, err := os.Stat(path); err == nil {
		_, err = f.Read(relative, hash)
		if err == nil || kind != "cache/http" {
			return relative, err
		}
		// Evictable cache corruption is repaired by the atomic write below.
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	// Persist new directory entries as well as the file's entry below.
	for parent := filepath.Dir(dir); ; parent = filepath.Dir(parent) {
		directory, err := os.Open(parent)
		if err != nil {
			return "", err
		}
		err = directory.Sync()
		directory.Close()
		if err != nil {
			return "", err
		}
		if parent == filepath.Clean(f.Root) || parent == filepath.Dir(parent) {
			break
		}
	}
	tmp, err := os.CreateTemp(dir, ".blob-")
	if err != nil {
		return "", err
	}
	defer os.Remove(tmp.Name())
	if _, err = tmp.Write(data); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Sync(); err != nil {
		tmp.Close()
		return "", err
	}
	if err = tmp.Close(); err != nil {
		return "", err
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return "", err
	}
	directory, err := os.Open(dir)
	if err != nil {
		return "", err
	}
	defer directory.Close()
	if err = directory.Sync(); err != nil {
		return "", err
	}
	return relative, nil
}
func (f Files) Read(relative, hash string) ([]byte, error) {
	clean := filepath.Clean(relative)
	if filepath.IsAbs(clean) || clean == "." || strings.HasPrefix(clean, "..") || !(strings.HasPrefix(clean, "texts"+string(filepath.Separator)) || strings.HasPrefix(clean, "digests"+string(filepath.Separator)) || strings.HasPrefix(clean, "cache/http"+string(filepath.Separator))) {
		return nil, errors.New("invalid stored file path")
	}
	data, err := os.ReadFile(filepath.Join(f.Root, clean))
	if err != nil {
		return nil, errors.New("збережений файл недоступний; перевірте каталог даних або резервну копію")
	}
	if fmt.Sprintf("%x", sha256.Sum256(data)) != hash {
		return nil, errors.New("збережений файл пошкоджено (hash mismatch)")
	}
	return data, nil
}

// File writes are synced before the transaction publishes their references.
// A rollback can leave an unreferenced blob, never a committed missing write.
