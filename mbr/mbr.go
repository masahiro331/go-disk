package mbr

import (
	"bytes"
	"encoding/binary"
	"io"
	"strconv"

	"github.com/masahiro331/go-disk/types"
	"golang.org/x/xerrors"
)

const (
	SIGNATURE        = 0xAA55
	Sector           = 512
	maxEBRChainDepth = 256
)

/*
# Master Boot Record Spec
https://uefi.org/sites/default/files/resources/UEFI%20Spec%202.8B%20May%202020.pdf
p. 112
Master Boot Record always 512 bytes.
+-------------------------------+
|         Name           | Byte |
+------------------------+------+
| Bootstrap Code Area    | 440  |
| UniqueMBRDiskSignature | 4    |
| Unknown                | 2    |
| Partion 1              | 16   |
| Partion 2              | 16   |
| Partion 3              | 16   |
| Partion 4              | 16   |
| Boot Recore Sigunature | 2    |
+-------------------------------+

# Partion Spec
+-------------------+------+----------------------------------------------------------+
|        Name       | Byte |                        Description                       |
+-------------------+------+----------------------------------------------------------+
| Boot Indicator    | 1    | Boot Partion                                             |
| Staring CHS value | 3    | Starting sector of the partition in Cylinder Head Sector |
| Partition type    | 1    | FileSystem used by the partition	                      |
| Ending CHS values | 3    | Ending sector of the partition in Cylinder Head Sector   |
| Starting Sector   | 4    | Starting sector of the active partition                  |
| Partition Size    | 4    | Represents partition size in sectors                     |
+-------------------+------+----------------------------------------------------------+


ref: https://www.ijais.org/research/volume10/number8/sadi-2016-ijais-451541.pdf
*/

var EmptyPartitionTable = xerrors.New("All partitions in the master boot record are empty")
var InvalidSignature = xerrors.New("Invalid master boot record signature")

type MasterBootRecord struct {
	BootCodeArea           [440]byte
	UniqueMBRDiskSignature [4]byte
	Unknown                [2]byte
	Partitions             [4]Partition
	Signature              uint16

	logicalPartitions []Partition
	currentIndex      int
	currentPartition  *Partition
	sectionReader     *io.SectionReader
}

type CHS [3]byte

type Partition struct {
	Boot     bool
	StartCHS CHS
	Type     byte
	EndCHS   CHS

	StartSector uint32
	Size        uint32
	index       int

	off           int64
	sectionReader *io.SectionReader
}

func (m *MasterBootRecord) Next() (types.Partition, error) {
	if m.currentPartition != nil {
		m.currentPartition.sectionReader = nil
	}

	total := len(m.Partitions) + len(m.logicalPartitions)
	if m.currentIndex >= total {
		return nil, io.EOF
	}

	var p *Partition
	if m.currentIndex < len(m.Partitions) {
		p = &m.Partitions[m.currentIndex]
	} else {
		p = &m.logicalPartitions[m.currentIndex-len(m.Partitions)]
	}
	m.currentIndex++

	offset := int64(p.GetStartSector()) * 512
	_, err := m.sectionReader.Seek(offset, 0)
	if err != nil {
		return nil, xerrors.Errorf("failed to seek partition(%d): %w", p.Index(), err)
	}
	p.sectionReader = io.NewSectionReader(m.sectionReader, offset, int64(p.GetSize()*512))

	m.currentPartition = p
	return p, nil
}

func (p Partition) Index() int {
	return p.index
}

func (p Partition) Name() string {
	// TODO: add extension with type

	return strconv.Itoa(int(p.index))
}

func (p Partition) GetType() []byte {
	return []byte{p.Type}
}

func (p Partition) GetStartSector() uint64 {
	return uint64(p.StartSector)
}

func (p Partition) Bootable() bool {
	return p.Boot
}

func (p Partition) GetSize() uint64 {
	return uint64(p.Size)
}

func (p Partition) GetSectionReader() io.SectionReader {
	return *p.sectionReader
}

func NewMasterBootRecord(sr *io.SectionReader) (*MasterBootRecord, error) {
	buf := make([]byte, Sector)
	size, err := sr.Read(buf)
	if err != nil {
		return nil, xerrors.Errorf("failed to read mbr error: %w", err)
	}
	if size != Sector {
		return nil, xerrors.Errorf("binary size error: actual(%d), expected(%d)", Sector, size)
	}

	r := bytes.NewReader(buf)
	mbr := MasterBootRecord{sectionReader: sr}

	if err := binary.Read(r, binary.LittleEndian, &mbr.BootCodeArea); err != nil {
		return nil, xerrors.Errorf("failed to parse boot code: %w", err)
	}

	if err := binary.Read(r, binary.LittleEndian, &mbr.UniqueMBRDiskSignature); err != nil {
		return nil, xerrors.Errorf("failed to parse unique MBR disk signature: %w", err)
	}

	if err := binary.Read(r, binary.LittleEndian, &mbr.Unknown); err != nil {
		return nil, xerrors.Errorf("failed to parse unknown: %w", err)
	}

	for i := 0; i < len(mbr.Partitions); i++ {
		if err := binary.Read(r, binary.LittleEndian, &mbr.Partitions[i].Boot); err != nil {
			return nil, xerrors.Errorf("failed to parse partition[%d] Boot: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &mbr.Partitions[i].StartCHS); err != nil {
			return nil, xerrors.Errorf("failed to parse partition[%d] StartCHS: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &mbr.Partitions[i].Type); err != nil {
			return nil, xerrors.Errorf("failed to parse partition[%d] Type: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &mbr.Partitions[i].EndCHS); err != nil {
			return nil, xerrors.Errorf("failed to parse partition[%d] EndCHS: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &mbr.Partitions[i].StartSector); err != nil {
			return nil, xerrors.Errorf("failed to parse partition[%d] StartSector: %w", i, err)
		}
		if err := binary.Read(r, binary.LittleEndian, &mbr.Partitions[i].Size); err != nil {
			return nil, xerrors.Errorf("failed to parse partition[%d] Size: %w", i, err)
		}
		mbr.Partitions[i].index = i
	}

	if err := binary.Read(r, binary.LittleEndian, &mbr.Signature); err != nil {
		return nil, xerrors.Errorf("failed to parse signature: %w", err)
	}
	if mbr.Signature != SIGNATURE {
		return nil, InvalidSignature
	}

	emptyPartitions := 0
	for i := 0; i < len(mbr.Partitions); i++ {
		if mbr.Partitions[i].Type == 0 {
			emptyPartitions++
			continue
		}
		if !isExtended(mbr.Partitions[i].Type) {
			continue
		}
		logicals, err := parseEBRChain(sr, mbr.Partitions[i].StartSector)
		if err != nil {
			return nil, xerrors.Errorf("failed to parse extended partition %d: %w", i, err)
		}
		if len(logicals) > 0 {
			mbr.logicalPartitions = append(mbr.logicalPartitions, logicals...)
		} else {
			// No valid EBR found; adjust partition to skip past the EBR area.
			if mbr.Partitions[i].Size > 2 {
				mbr.Partitions[i].StartSector += 2
				mbr.Partitions[i].Size -= 2
			} else {
				mbr.Partitions[i].Size = 0
			}
		}
	}

	// Assign indices to logical partitions (starting at 4)
	for i := range mbr.logicalPartitions {
		mbr.logicalPartitions[i].index = len(mbr.Partitions) + i
	}

	if emptyPartitions == len(mbr.Partitions) {
		return nil, EmptyPartitionTable
	}

	return &mbr, nil
}

// isExtended reports whether t is an extended partition type whose EBR chain holds logical partitions:
// 0x05 (CHS addressing), 0x0f (LBA addressing) or 0x85 (Linux extended).
// These are the types the Linux kernel treats as extended; hidden types such as 0x15 and 0x1f are not.
func isExtended(t byte) bool {
	return t == 0x05 || t == 0x0f || t == 0x85
}

func (p Partition) IsSupported() bool {
	return true
}

func parsePartitionEntry(buf []byte) Partition {
	if len(buf) < 16 {
		return Partition{}
	}
	return Partition{
		Boot:        buf[0] != 0,
		StartCHS:    CHS{buf[1], buf[2], buf[3]},
		Type:        buf[4],
		EndCHS:      CHS{buf[5], buf[6], buf[7]},
		StartSector: binary.LittleEndian.Uint32(buf[8:12]),
		Size:        binary.LittleEndian.Uint32(buf[12:16]),
	}
}

// parseEBRChain traverses the Extended Boot Record chain starting at
// extStartSector and returns all logical partitions found.
// Each EBR has the same 512-byte structure as an MBR:
//   - Entry 0: logical partition (StartSector relative to this EBR)
//   - Entry 1: next EBR pointer (StartSector relative to extStartSector)
//
// I/O failures (seek/read/short read) are returned as errors so the
// caller can distinguish a malformed/truncated image from a legitimate
// "no EBR" case (signature mismatch), which terminates the chain
// without an error and lets the caller apply a fallback.
func parseEBRChain(sr *io.SectionReader, extStartSector uint32) ([]Partition, error) {
	var partitions []Partition
	ebrSector := extStartSector
	visited := make(map[uint32]bool)
	buf := make([]byte, Sector)

	for hops := 0; hops < maxEBRChainDepth; hops++ {
		if visited[ebrSector] {
			break
		}
		visited[ebrSector] = true

		ebrOffset := int64(ebrSector) * Sector
		if _, err := sr.Seek(ebrOffset, 0); err != nil {
			return nil, xerrors.Errorf("failed to seek EBR at sector %d: %w", ebrSector, err)
		}

		if _, err := io.ReadFull(sr, buf); err != nil {
			return nil, xerrors.Errorf("failed to read EBR at sector %d: %w", ebrSector, err)
		}

		sig := binary.LittleEndian.Uint16(buf[510:512])
		if sig != SIGNATURE {
			break
		}

		// Entry 0: logical partition (StartSector relative to this EBR)
		entry0 := parsePartitionEntry(buf[446:462])
		if entry0.Type != 0 {
			entry0.StartSector += ebrSector
			partitions = append(partitions, entry0)
		}

		// Entry 1: next EBR pointer (StartSector relative to extended partition start)
		entry1 := parsePartitionEntry(buf[462:478])
		if entry1.Type == 0 || entry1.StartSector == 0 {
			break
		}
		ebrSector = extStartSector + entry1.StartSector
	}

	return partitions, nil
}
