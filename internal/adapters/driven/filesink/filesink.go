// Package filesink implements ports.FileSink with files in one directory:
// saved logs are the user's, readable by them only, and never overwrite a
// file.
package filesink

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// maxTries bounds the suffixes tried when a name is taken (-1, -2, …).
const maxTries = 100

// Dir saves files in a directory.
type Dir struct {
	// Path is the directory; "" is the current one. It must exist: a
	// missing directory is an error, not created behind the user's back.
	Path string
}

// Save implements ports.FileSink.
func (d Dir) Save(ctx context.Context, name string, write func(io.Writer) error) (string, error) {
	if name == "" || name != filepath.Base(name) || strings.HasPrefix(name, ".") {
		return "", fmt.Errorf("invalid file name %q", name)
	}
	dir := d.Path
	if dir == "" {
		dir = "."
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return "", fmt.Errorf("save directory %s does not exist (ui.yaml save.dir)", dir)
	}
	f, path, err := create(dir, name)
	if err != nil {
		return "", err
	}
	w := bufio.NewWriterSize(f, 64<<10)
	err = write(w)
	if err == nil {
		err = ctx.Err()
	}
	if err == nil {
		err = w.Flush()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(path) // no half-written file left behind
		return "", fmt.Errorf("save %s: %w", path, err)
	}
	if abs, aerr := filepath.Abs(path); aerr == nil {
		path = abs
	}
	return path, nil
}

// create opens a new file named name in dir, or name-1, name-2, … before
// the extension when it exists, with mode 0600.
func create(dir, name string) (*os.File, string, error) {
	ext := filepath.Ext(name)
	stem := strings.TrimSuffix(name, ext)
	for i := range maxTries {
		n := name
		if i > 0 {
			n = fmt.Sprintf("%s-%d%s", stem, i, ext)
		}
		path := filepath.Join(dir, n)
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err == nil {
			return f, path, nil
		}
		if !errors.Is(err, fs.ErrExist) {
			return nil, "", fmt.Errorf("create %s: %w", path, err)
		}
	}
	return nil, "", fmt.Errorf("%s: %d files of that name already exist", filepath.Join(dir, name), maxTries)
}
