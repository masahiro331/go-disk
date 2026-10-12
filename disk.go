package disk

import (
	"io"

	"github.com/masahiro331/go-disk/fs"
	"github.com/masahiro331/go-disk/gpt"
	"github.com/masahiro331/go-disk/mbr"
	"github.com/masahiro331/go-disk/types"
	"golang.org/x/xerrors"
)

// NewDriver parses the partition table of r.
// The disk is GPT if any MBR entry has type 0xEE, and MBR otherwise. Linux also ignores MBR partitions in that case.
// r is read only with ReadAt, and partitions returned by the driver can be read concurrently
// if r supports concurrent ReadAt calls. *io.SectionReader satisfies io.ReaderAt.
func NewDriver(r io.ReaderAt, checkFsFuncs ...fs.CheckFsFunc) (types.Driver, error) {
	m, err := mbr.NewMasterBootRecord(r)
	if err != nil {
		if xerrors.Is(err, mbr.EmptyPartitionTable) || xerrors.Is(err, mbr.InvalidSignature) {
			ok, err := fs.CheckFileSystems(r, checkFsFuncs)
			if err != nil {
				return nil, xerrors.Errorf("failed to check filesystem: %w", err)
			}
			if ok {
				return fs.NewDirectFileSystem(r)
			}
		}
		return nil, xerrors.Errorf("failed to new MBR: %w", err)
	}

	if !hasGPTProtectiveEntry(m) {
		return m, nil
	}

	g, err := gpt.NewGUIDPartitionTable(r)
	if err != nil {
		return nil, xerrors.Errorf("failed to parse GUID partition table: %w", err)
	}

	return g, nil
}

func hasGPTProtectiveEntry(m *mbr.MasterBootRecord) bool {
	for _, p := range m.Partitions {
		if p.Type == 0xEE {
			return true
		}
	}
	return false
}
