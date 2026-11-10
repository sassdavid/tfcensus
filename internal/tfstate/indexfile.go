package tfstate

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// WriteIndexFile writes v as JSON to path, compressed with zstd for a .zst name and with gzip for
// a .gz name. The file is written next to path and renamed, so readers never see half of it.
func WriteIndexFile(path string, v any) (err error) {
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("create index file: %w", err)
	}
	defer func() {
		if err != nil {
			_ = tmp.Close()
			_ = os.Remove(tmp.Name())
		}
	}()
	w, closeW, err := compressor(path, tmp)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	if err = enc.Encode(v); err != nil {
		return fmt.Errorf("encode index: %w", err)
	}
	if err = closeW(); err != nil {
		return fmt.Errorf("compress index: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("write index file: %w", err)
	}
	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("write index file: %w", err)
	}
	return nil
}

// ReadIndexFile decodes the JSON in path into v, decompressing by extension like [WriteIndexFile].
func ReadIndexFile(path string, v any) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open index file: %w", err)
	}
	defer func() { _ = f.Close() }()
	var r io.Reader = f
	switch {
	case strings.HasSuffix(path, ".zst"):
		zr, zerr := zstd.NewReader(f)
		if zerr != nil {
			return fmt.Errorf("open index file: %w", zerr)
		}
		defer zr.Close()
		r = zr
	case strings.HasSuffix(path, ".gz"):
		gr, gerr := gzip.NewReader(f)
		if gerr != nil {
			return fmt.Errorf("open index file: %w", gerr)
		}
		defer func() { _ = gr.Close() }()
		r = gr
	}
	if err = json.NewDecoder(r).Decode(v); err != nil {
		return fmt.Errorf("decode index file: %w", err)
	}
	return nil
}

func compressor(path string, w io.Writer) (io.Writer, func() error, error) {
	switch {
	case strings.HasSuffix(path, ".zst"):
		zw, err := zstd.NewWriter(w)
		if err != nil {
			return nil, nil, fmt.Errorf("compress index: %w", err)
		}
		return zw, zw.Close, nil
	case strings.HasSuffix(path, ".gz"):
		gw := gzip.NewWriter(w)
		return gw, gw.Close, nil
	default:
		return w, func() error { return nil }, nil
	}
}
