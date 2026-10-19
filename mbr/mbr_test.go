package mbr_test

import (
	"encoding/binary"
	"io"
	"os"
	"testing"

	"github.com/masahiro331/go-disk/mbr"
)

func TestNewMasterBootRecord(t *testing.T) {
	tests := []struct {
		name      string
		inputFile string
		want      *mbr.MasterBootRecord
		wantErr   bool
	}{
		{
			name:      "happy path",
			inputFile: "testdata/mbr.bin", // built by "make mbr.bin" from m1 mac
			want: &mbr.MasterBootRecord{
				Partitions: [4]mbr.Partition{
					{
						Boot:        true,
						Type:        0xAB,
						StartSector: 63,
						Size:        16384,
					},
					{
						Boot:        false,
						Type:        0xAF,
						StartSector: 16447, // Partition[0].StartSector + Partition[0].Size
						Size:        16322,
					},
				},
				Signature: 0xAA55,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f, err := os.Open(tt.inputFile)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()

			info, err := f.Stat()
			if err != nil {
				t.Fatal(err)
			}

			got, err := mbr.NewMasterBootRecord(io.NewSectionReader(f, 0, info.Size()))
			if (err != nil) != tt.wantErr {
				t.Errorf("NewMasterBootRecord() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got.Signature != tt.want.Signature {
				t.Errorf("MasterBootRecord.Signature got = %v, want %v", got.Signature, tt.want.Signature)
			}
			for i := 0; i < 4; i++ {
				if got.Partitions[i].Type != tt.want.Partitions[i].Type {
					t.Errorf("MasterBootRecord.Partitions[i].Type got = %v, want %v", got.Partitions[i].Type, tt.want.Partitions[i].Type)
				}
				if got.Partitions[i].Boot != tt.want.Partitions[i].Boot {
					t.Errorf("MasterBootRecord.Partitions[i].Boot got = %v, want %v", got.Partitions[i].Boot, tt.want.Partitions[i].Boot)
				}
				if got.Partitions[i].Size != tt.want.Partitions[i].Size {
					t.Errorf("MasterSizeRecord.Partitions[i].Size got = %v, want %v", got.Partitions[i].Size, tt.want.Partitions[i].Size)
				}
				if got.Partitions[i].StartSector != tt.want.Partitions[i].StartSector {
					t.Errorf("MasterStartSectorRecord.Partitions[i].StartSector got = %v, want %v", got.Partitions[i].StartSector, tt.want.Partitions[i].StartSector)
				}
			}
		})
	}
}

func TestNewMasterBootRecord_FieldParseOrder(t *testing.T) {
	// Build a minimal valid MBR in memory to verify that fields are
	// parsed from the correct byte positions per the MBR specification:
	//   bytes 0-439:   BootCodeArea
	//   bytes 440-443: UniqueMBRDiskSignature
	//   bytes 444-445: Unknown
	//   bytes 446-509: Partitions
	//   bytes 510-511: Signature
	buf := make([]byte, 512)

	// BootCodeArea: set recognizable bytes at the start and end
	buf[0] = 0xEB // typical JMP instruction
	buf[1] = 0x5A
	buf[439] = 0xFF // last byte of BootCodeArea

	// UniqueMBRDiskSignature (bytes 440-443)
	buf[440] = 0xDE
	buf[441] = 0xAD
	buf[442] = 0xBE
	buf[443] = 0xEF

	// Unknown (bytes 444-445)
	buf[444] = 0xCA
	buf[445] = 0xFE

	// Partitions (bytes 446-509): set at least one non-empty partition
	// to avoid EmptyPartitionTable error when PR #4 is merged.
	// Partition 0: Type = 0x83 (Linux) at byte 450, StartSector at 454, Size at 458
	buf[450] = 0x83
	binary.LittleEndian.PutUint32(buf[454:], 1)
	binary.LittleEndian.PutUint32(buf[458:], 1)

	// Signature (bytes 510-511)
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
	got, err := mbr.NewMasterBootRecord(sr)
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	// Verify BootCodeArea is parsed from bytes 0-439
	if got.BootCodeArea[0] != 0xEB || got.BootCodeArea[1] != 0x5A {
		t.Errorf("BootCodeArea[0:2] = %x, want eb5a", got.BootCodeArea[0:2])
	}
	if got.BootCodeArea[439] != 0xFF {
		t.Errorf("BootCodeArea[439] = %x, want ff", got.BootCodeArea[439])
	}

	// Verify UniqueMBRDiskSignature is parsed from bytes 440-443
	wantSig := [4]byte{0xDE, 0xAD, 0xBE, 0xEF}
	if got.UniqueMBRDiskSignature != wantSig {
		t.Errorf("UniqueMBRDiskSignature = %x, want %x", got.UniqueMBRDiskSignature, wantSig)
	}

	// Verify Unknown is parsed from bytes 444-445
	wantUnknown := [2]byte{0xCA, 0xFE}
	if got.Unknown != wantUnknown {
		t.Errorf("Unknown = %x, want %x", got.Unknown, wantUnknown)
	}
}

func TestMasterBootRecord_Next(t *testing.T) {
	// Build an MBR with 2 used partitions and 2 empty ones,
	// then verify Next() iteration behavior and SectionReader creation.
	//
	// Partition layout:
	//   [0] StartSector=2, Size=4
	//   [1] StartSector=8, Size=2
	//   [2] empty (StartSector=0, Size=0)
	//   [3] empty (StartSector=0, Size=0)
	diskSize := 10 * 512 // enough to cover partition 1 end (sector 8+2=10)
	buf := make([]byte, diskSize)

	// Write marker bytes in each partition's data area so we can
	// verify that SectionReader reads from the correct offset.
	buf[2*512] = 0xAA // first byte of partition 0 data
	buf[8*512] = 0xBB // first byte of partition 1 data

	// --- Write MBR at sector 0 (bytes 0-511) ---
	writePartitionEntry(buf[446:], true, 0x83, 2, 4)
	writePartitionEntry(buf[462:], false, 0x82, 8, 2)
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
	m, err := mbr.NewMasterBootRecord(sr)
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	expected := []struct {
		size   uint64
		marker byte // expected first byte from SectionReader
	}{
		{4, 0xAA},
		{2, 0xBB},
		{0, 0x00},
		{0, 0x00},
	}

	for i, exp := range expected {
		p, err := m.Next()
		if err != nil {
			t.Fatalf("Next() partition %d: unexpected error: %v", i, err)
		}

		// Verify SectionReader size
		psr := p.GetSectionReader()
		wantSize := int64(exp.size) * 512
		if psr.Size() != wantSize {
			t.Errorf("partition %d: SectionReader.Size() = %d, want %d", i, psr.Size(), wantSize)
		}

		// Verify SectionReader reads from the correct offset
		if exp.size > 0 {
			b := make([]byte, 1)
			if _, err := psr.Read(b); err != nil {
				t.Fatalf("partition %d: SectionReader.Read() error: %v", i, err)
			}
			if b[0] != exp.marker {
				t.Errorf("partition %d: first byte = %x, want %x", i, b[0], exp.marker)
			}
		}
	}

	// After all 4 partitions, Next() must return io.EOF
	_, err = m.Next()
	if err != io.EOF {
		t.Errorf("Next() after all partitions: got %v, want io.EOF", err)
	}
}

func TestMasterBootRecord_Next_ExtendedSingleLogical(t *testing.T) {
	// Disk layout:
	//   Sector 0:    MBR
	//   Sector 2-5:  Primary partition 0 (Type 0x83, Size 4)
	//   Sector 10:   EBR 1
	//   Sector 11-15: Logical partition 4 (Type 0x83, Size 5)
	diskSize := 30 * 512
	buf := make([]byte, diskSize)

	// Marker bytes
	buf[2*512] = 0xAA  // primary partition 0 data
	buf[11*512] = 0xCC // logical partition 4 data

	// MBR at sector 0
	writePartitionEntry(buf[446:], true, 0x83, 2, 4)    // partition 0: primary
	writePartitionEntry(buf[462:], false, 0x0f, 10, 20) // partition 1: extended (LBA)
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

	// EBR 1 at sector 10
	ebr1 := 10 * 512
	writePartitionEntry(buf[ebr1+446:], false, 0x83, 1, 5) // entry 0: logical (abs sector=10+1=11)
	// entry 1: no next EBR (type=0)
	binary.LittleEndian.PutUint16(buf[ebr1+510:], 0xAA55)

	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
	m, err := mbr.NewMasterBootRecord(sr)
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	expected := []struct {
		size   uint64
		marker byte
	}{
		{4, 0xAA},  // primary 0
		{20, 0x00}, // extended LBA (container)
		{0, 0x00},  // empty
		{0, 0x00},  // empty
		{5, 0xCC},  // logical 4
	}

	for i, exp := range expected {
		p, err := m.Next()
		if err != nil {
			t.Fatalf("Next() call %d: unexpected error: %v", i, err)
		}
		if p.GetSize() != exp.size {
			t.Errorf("partition %d: GetSize() = %d, want %d", i, p.GetSize(), exp.size)
		}
		if exp.size > 0 && exp.marker != 0x00 {
			psr := p.GetSectionReader()
			b := make([]byte, 1)
			if _, err := psr.Read(b); err != nil {
				t.Fatalf("partition %d: SectionReader.Read() error: %v", i, err)
			}
			if b[0] != exp.marker {
				t.Errorf("partition %d: first byte = %x, want %x", i, b[0], exp.marker)
			}
		}
	}

	_, err = m.Next()
	if err != io.EOF {
		t.Errorf("Next() after all partitions: got %v, want io.EOF", err)
	}
}

func TestMasterBootRecord_Next_ExtendedTypes(t *testing.T) {
	tests := []struct {
		name     string
		typeByte byte
	}{
		{name: "CHS", typeByte: 0x05},
		{name: "LBA", typeByte: 0x0f},
		{name: "Linux", typeByte: 0x85},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 30*512)
			writePartitionEntry(buf[446:], true, 0x83, 2, 4)
			writePartitionEntry(buf[462:], false, tt.typeByte, 10, 20)
			binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

			ebr1 := 10 * 512
			writePartitionEntry(buf[ebr1+446:], false, 0x83, 1, 5)
			binary.LittleEndian.PutUint16(buf[ebr1+510:], 0xAA55)

			m, err := mbr.NewMasterBootRecord(io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf))))
			if err != nil {
				t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
			}

			// Next() returns the 4 primary entries first, then the logical partitions.
			var logicalStarts []uint64
			for i := 0; ; i++ {
				p, err := m.Next()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("Next() unexpected error: %v", err)
				}
				if i >= len(m.Partitions) {
					logicalStarts = append(logicalStarts, p.GetStartSector())
				}
			}
			if len(logicalStarts) != 1 || logicalStarts[0] != 11 {
				t.Errorf("logical partition start sectors = %v, want [11]", logicalStarts)
			}
		})
	}
}

func TestMasterBootRecord_Next_ExtendedMultipleLogicals(t *testing.T) {
	// Disk layout:
	//   Sector 0:     MBR
	//   Sector 2-5:   Primary partition 0 (Type 0x83, Size 4)
	//   Sector 10:    EBR 1
	//   Sector 11-15: Logical partition 4 (Type 0x83, Size 5)
	//   Sector 20:    EBR 2
	//   Sector 21-25: Logical partition 5 (Type 0x82, Size 5)
	diskSize := 50 * 512
	buf := make([]byte, diskSize)

	// Marker bytes
	buf[2*512] = 0xAA  // primary 0
	buf[11*512] = 0xBB // logical 4
	buf[21*512] = 0xCC // logical 5

	// MBR at sector 0
	writePartitionEntry(buf[446:], true, 0x83, 2, 4)    // partition 0: primary
	writePartitionEntry(buf[462:], false, 0x05, 10, 40) // partition 1: extended
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

	// EBR 1 at sector 10
	ebr1 := 10 * 512
	writePartitionEntry(buf[ebr1+446:], false, 0x83, 1, 5)   // entry 0: logical (abs=11)
	writePartitionEntry(buf[ebr1+462:], false, 0x05, 10, 16) // entry 1: next EBR at ext_start+10=20
	binary.LittleEndian.PutUint16(buf[ebr1+510:], 0xAA55)

	// EBR 2 at sector 20
	ebr2 := 20 * 512
	writePartitionEntry(buf[ebr2+446:], false, 0x82, 1, 5) // entry 0: logical (abs=21)
	// entry 1: no next EBR
	binary.LittleEndian.PutUint16(buf[ebr2+510:], 0xAA55)

	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
	m, err := mbr.NewMasterBootRecord(sr)
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	expected := []struct {
		typB   byte
		size   uint64
		marker byte
	}{
		{0x83, 4, 0xAA},  // primary 0
		{0x05, 40, 0x00}, // extended
		{0x00, 0, 0x00},  // empty
		{0x00, 0, 0x00},  // empty
		{0x83, 5, 0xBB},  // logical 4
		{0x82, 5, 0xCC},  // logical 5
	}

	for i, exp := range expected {
		p, err := m.Next()
		if err != nil {
			t.Fatalf("Next() call %d: unexpected error: %v", i, err)
		}
		if p.GetSize() != exp.size {
			t.Errorf("partition %d: GetSize() = %d, want %d", i, p.GetSize(), exp.size)
		}
		gotType := p.GetType()
		if len(gotType) != 1 || gotType[0] != exp.typB {
			t.Errorf("partition %d: GetType() = %x, want %x", i, gotType, exp.typB)
		}
		if exp.size > 0 && exp.marker != 0x00 {
			psr := p.GetSectionReader()
			b := make([]byte, 1)
			if _, err := psr.Read(b); err != nil {
				t.Fatalf("partition %d: SectionReader.Read() error: %v", i, err)
			}
			if b[0] != exp.marker {
				t.Errorf("partition %d: first byte = %x, want %x", i, b[0], exp.marker)
			}
		}
	}

	_, err = m.Next()
	if err != io.EOF {
		t.Errorf("Next() after all partitions: got %v, want io.EOF", err)
	}
}

func TestMasterBootRecord_Next_ExtendedInvalidEBR(t *testing.T) {
	// Extended partition with no valid EBR (invalid signature).
	// Should fall back to adjusting StartSector +2, Size -2.
	diskSize := 30 * 512
	buf := make([]byte, diskSize)

	writePartitionEntry(buf[446:], true, 0x83, 2, 4)    // partition 0: primary
	writePartitionEntry(buf[462:], false, 0x05, 10, 20) // partition 1: extended
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

	// Sector 10: no valid EBR signature (leave as zeros)

	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
	m, err := mbr.NewMasterBootRecord(sr)
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	// Extended partition should have StartSector adjusted to 12, Size to 18
	expected := []struct {
		size uint64
	}{
		{4},  // primary 0
		{18}, // extended (adjusted: 20-2)
		{0},  // empty
		{0},  // empty
	}

	for i, exp := range expected {
		p, err := m.Next()
		if err != nil {
			t.Fatalf("Next() call %d: unexpected error: %v", i, err)
		}
		if p.GetSize() != exp.size {
			t.Errorf("partition %d: GetSize() = %d, want %d", i, p.GetSize(), exp.size)
		}
	}

	// Only 4 primary partitions, no logical
	_, err = m.Next()
	if err != io.EOF {
		t.Errorf("Next() after all partitions: got %v, want io.EOF", err)
	}
}

func TestMasterBootRecord_Next_ExtendedCircularReference(t *testing.T) {
	// EBR chain with circular reference: EBR 1 points to itself.
	diskSize := 30 * 512
	buf := make([]byte, diskSize)

	writePartitionEntry(buf[446:], true, 0x83, 2, 4)    // partition 0: primary
	writePartitionEntry(buf[462:], false, 0x05, 10, 20) // partition 1: extended
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

	// EBR 1 at sector 10: entry 1 points back to sector 10 (circular)
	ebr1 := 10 * 512
	writePartitionEntry(buf[ebr1+446:], false, 0x83, 1, 5)  // entry 0: logical
	writePartitionEntry(buf[ebr1+462:], false, 0x05, 0, 20) // entry 1: points to ext_start+0=10 (self)
	binary.LittleEndian.PutUint16(buf[ebr1+510:], 0xAA55)

	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
	m, err := mbr.NewMasterBootRecord(sr)
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	// Should get 4 primary + 1 logical (chain stops at circular reference)
	count := 0
	for {
		_, err := m.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("Next() unexpected error: %v", err)
		}
		count++
	}
	if count != 5 {
		t.Errorf("partition count = %d, want 5 (4 primary + 1 logical)", count)
	}
}

func TestMasterBootRecord_Next_ExtendedSkippedEntries(t *testing.T) {
	tests := []struct {
		name       string
		entry0Type byte
		entry0Size uint32
		entry1Type byte
		entry1Size uint32
		want       int // logical partitions
	}{
		{name: "valid logical and extended link", entry0Type: 0x83, entry0Size: 5, entry1Type: 0x05, entry1Size: 10, want: 2},
		{name: "zero-sized entry 0", entry0Type: 0x83, entry0Size: 0, entry1Type: 0x05, entry1Size: 10, want: 1},
		{name: "extended entry 0", entry0Type: 0x05, entry0Size: 5, entry1Type: 0x05, entry1Size: 10, want: 1},
		{name: "zero-sized entry 0 without link", entry0Type: 0x83, entry0Size: 0, entry1Type: 0x00, entry1Size: 0, want: 0},
		// Entry 1 always points to a valid EBR, so following a link that must be skipped adds a partition.
		{name: "non-extended link", entry0Type: 0x83, entry0Size: 5, entry1Type: 0x83, entry1Size: 10, want: 1},
		{name: "zero-sized link", entry0Type: 0x83, entry0Size: 5, entry1Type: 0x05, entry1Size: 0, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := make([]byte, 40*512)
			writePartitionEntry(buf[446:], true, 0x83, 2, 4)
			writePartitionEntry(buf[462:], false, 0x05, 10, 30)
			binary.LittleEndian.PutUint16(buf[510:], 0xAA55)

			ebr1 := 10 * 512
			writePartitionEntry(buf[ebr1+446:], false, tt.entry0Type, 1, tt.entry0Size)
			writePartitionEntry(buf[ebr1+462:], false, tt.entry1Type, 10, tt.entry1Size)
			binary.LittleEndian.PutUint16(buf[ebr1+510:], 0xAA55)

			ebr2 := 20 * 512
			writePartitionEntry(buf[ebr2+446:], false, 0x83, 1, 5)
			binary.LittleEndian.PutUint16(buf[ebr2+510:], 0xAA55)

			sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))
			m, err := mbr.NewMasterBootRecord(sr)
			if err != nil {
				t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
			}
			count := 0
			for {
				if _, err := m.Next(); err == io.EOF {
					break
				} else if err != nil {
					t.Fatalf("Next() unexpected error: %v", err)
				}
				count++
			}
			if got := count - len(m.Partitions); got != tt.want {
				t.Errorf("logical partitions = %d, want %d", got, tt.want)
			}
		})
	}
}

func writePartitionEntry(dst []byte, boot bool, typeByte byte, startSector, size uint32) {
	if boot {
		dst[0] = 0x80
	}
	// StartCHS (bytes 1-3): leave zeros
	dst[4] = typeByte
	// EndCHS (bytes 5-7): leave zeros
	binary.LittleEndian.PutUint32(dst[8:], startSector)
	binary.LittleEndian.PutUint32(dst[12:], size)
}

type readerAt struct{ data []byte }

func newReaderAt(data []byte) *readerAt { return &readerAt{data: data} }

func (r *readerAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= int64(len(r.data)) {
		return 0, io.EOF
	}
	n := copy(p, r.data[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}
