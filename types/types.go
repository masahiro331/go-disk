package types

import "io"

type Driver interface {
	Next() (Partition, error)
}

type Partition interface {
	// ReadAt reads from the partition, bounded to its range.
	io.ReaderAt

	Bootable() bool
	GetStartSector() uint64
	Name() string
	GetType() []byte
	GetSize() uint64

	IsSupported() bool
	GetSectionReader() io.SectionReader
}
