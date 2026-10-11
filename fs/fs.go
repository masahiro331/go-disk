package fs

import (
	"io"
	"math"

	"github.com/masahiro331/go-disk/types"
)

var (
	_ types.Driver    = &DirectFileSystem{}
	_ types.Partition = &DirectFileSystemPartition{}
)

type DirectFileSystem struct {
	Partition *DirectFileSystemPartition
}

func (d *DirectFileSystem) Next() (types.Partition, error) {
	if d.Partition == nil {
		return nil, io.EOF
	}
	partition := d.Partition
	d.Partition = nil

	return partition, nil
}

type DirectFileSystemPartition struct {
	sectionReader *io.SectionReader
}

func (p DirectFileSystemPartition) Name() string {
	return "0"
}

func (p DirectFileSystemPartition) GetType() []byte {
	return []byte{}
}

func (p DirectFileSystemPartition) GetStartSector() uint64 {
	return uint64(0)
}

func (p DirectFileSystemPartition) Bootable() bool {
	return false
}

// GetSize returns 0 because DirectFileSystem has no partition table
// and sector-based size is not applicable.
func (p DirectFileSystemPartition) GetSize() uint64 {
	return 0
}

// ReadAt reads from the filesystem. Reads are bounded to the size determined by NewDirectFileSystem.
// It is safe for concurrent use if the underlying disk reader is.
func (p DirectFileSystemPartition) ReadAt(b []byte, off int64) (int, error) {
	return p.sectionReader.ReadAt(b, off)
}

// GetSectionReader returns a new SectionReader over the filesystem.
// Each call returns an independent reader with its own read offset.
func (p DirectFileSystemPartition) GetSectionReader() io.SectionReader {
	return *p.sectionReader
}

func (p DirectFileSystemPartition) IsSupported() bool {
	return true
}

// NewDirectFileSystem treats the whole of r as a single filesystem.
// If r has a Size method (e.g. *io.SectionReader), reads are bounded to that size.
func NewDirectFileSystem(r io.ReaderAt) (*DirectFileSystem, error) {
	return &DirectFileSystem{
		Partition: &DirectFileSystemPartition{
			sectionReader: newSectionReader(r),
		},
	}, nil
}

type CheckFsFunc func(r io.Reader) bool

// CheckFileSystems reports whether any of checkFsFuncs recognizes a filesystem at the start of r.
// Each function reads through its own reader, so the checks do not share a read offset.
func CheckFileSystems(r io.ReaderAt, checkFsFuncs []CheckFsFunc) (bool, error) {
	for _, checkFsFunc := range checkFsFuncs {
		if checkFsFunc(newSectionReader(r)) {
			return true, nil
		}
	}
	return false, nil
}

func newSectionReader(r io.ReaderAt) *io.SectionReader {
	size := int64(math.MaxInt64)
	if s, ok := r.(interface{ Size() int64 }); ok {
		size = s.Size()
	}
	return io.NewSectionReader(r, 0, size)
}
