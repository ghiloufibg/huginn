package ports

import (
	"context"
	"io"
)

// FileSink saves exported text (M9.2): write writes the content, streamed;
// Save returns the path of the new file. It never overwrites a file.
type FileSink interface {
	Save(ctx context.Context, name string, write func(io.Writer) error) (path string, err error)
}
