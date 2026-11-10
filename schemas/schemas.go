package schemas

import (
	"embed"
	"fmt"
	"io/fs"
)

// Embedded schema file names.
const (
	ConfigV1 = "config-v1.json"
	ReportV1 = "report-v1.json"
)

//go:embed *.json
var files embed.FS

// Read returns the raw bytes of the named schema file.
func Read(name string) ([]byte, error) {
	b, err := fs.ReadFile(files, name)
	if err != nil {
		return nil, fmt.Errorf("read embedded schema %s: %w", name, err)
	}
	return b, nil
}
