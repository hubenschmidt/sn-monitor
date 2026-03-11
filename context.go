package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	contextMaxFiles   = 50
	contextMaxPerFile = 50 * 1024
	contextMaxTotal   = 200 * 1024
)

func isBinary(data []byte) bool {
	limit := len(data)
	if limit > 512 {
		limit = 512
	}
	for i := 0; i < limit; i++ {
		if data[i] == 0 {
			return true
		}
	}
	return false
}

func readContextFile(path string, maxSize int64) (string, bool) {
	info, err := os.Stat(path)
	if err != nil {
		return "", false
	}
	if info.Size() > maxSize {
		fmt.Fprintf(os.Stderr, "context: skipping %s (too large: %d bytes)\n", path, info.Size())
		return "", false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	if isBinary(data) {
		fmt.Fprintf(os.Stderr, "context: skipping %s (binary file)\n", path)
		return "", false
	}
	return string(data), true
}

func readContextPath(path string) string {
	if path == "" {
		return ""
	}
	info, err := os.Stat(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "context: cannot stat %s: %v\n", path, err)
		return ""
	}
	if !info.IsDir() {
		return readSingleFileContext(path)
	}
	return readDirContext(path)
}

func readSingleFileContext(path string) string {
	content, ok := readContextFile(path, contextMaxPerFile)
	if !ok {
		return ""
	}
	var buf strings.Builder
	buf.WriteString("--- File: ")
	buf.WriteString(filepath.Base(path))
	buf.WriteString(" ---\n")
	buf.WriteString(content)
	buf.WriteString("\n\n")
	return buf.String()
}

func readDirContext(dir string) string {
	var buf strings.Builder
	totalSize := 0
	fileCount := 0

	filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() || strings.HasPrefix(d.Name(), ".") {
			return nil
		}
		if fileCount >= contextMaxFiles {
			fmt.Fprintf(os.Stderr, "context: file limit reached (%d files)\n", contextMaxFiles)
			return filepath.SkipAll
		}
		content, ok := readContextFile(path, contextMaxPerFile)
		if !ok {
			return nil
		}
		if totalSize+len(content) > contextMaxTotal {
			fmt.Fprintf(os.Stderr, "context: total size limit reached (%d bytes)\n", contextMaxTotal)
			return filepath.SkipAll
		}
		rel, _ := filepath.Rel(dir, path)
		buf.WriteString("--- File: ")
		buf.WriteString(rel)
		buf.WriteString(" ---\n")
		buf.WriteString(content)
		buf.WriteString("\n\n")
		totalSize += len(content)
		fileCount++
		return nil
	})

	return buf.String()
}
