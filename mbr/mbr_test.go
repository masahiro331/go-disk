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
