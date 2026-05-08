package ipfs

import (
	"io"

	"github.com/ipfs/boxo/files"
)

// namedReader оборачивает io.Reader в files.File с именем.
type namedReader struct {
	name string
	r    io.Reader
}

func newNamedReader(name string, r io.Reader) files.File {
	return files.NewReaderFile(r)
}
