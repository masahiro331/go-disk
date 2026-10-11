package mbr_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/rand"
	"os"
	"sync"
	"testing"

	"github.com/masahiro331/go-disk/mbr"
	"github.com/masahiro331/go-disk/types"
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

// newPatternedMBRDisk builds a disk whose bytes after the MBR are a pattern of their absolute offset,
// so a read from the wrong position is detected.
//
// Partition layout:
//
//	[0] StartSector=2, Size=4
//	[1] StartSector=6, Size=3
func newPatternedMBRDisk() []byte {
	buf := make([]byte, 10*mbr.Sector)
	for i := mbr.Sector; i < len(buf); i++ {
		buf[i] = byte(i % 251)
	}
	writePartitionEntry(buf[446:], true, 0x83, 2, 4)
	writePartitionEntry(buf[462:], false, 0x83, 6, 3)
	binary.LittleEndian.PutUint16(buf[510:], 0xAA55)
	return buf
}

func TestPartition_ConcurrentReadAt(t *testing.T) {
	disk := newPatternedMBRDisk()
	m, err := mbr.NewMasterBootRecord(io.NewSectionReader(newReaderAt(disk), 0, int64(len(disk))))
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}

	// Partitions must be readable before Next() reaches them.
	b := make([]byte, 1)
	if _, err := m.Partitions[1].ReadAt(b, 0); err != nil || b[0] != disk[6*mbr.Sector] {
		t.Fatalf("Partitions[1].ReadAt() before Next() = (%x, %v), want (%x, nil)", b[0], err, disk[6*mbr.Sector])
	}

	// Hold two partitions returned by consecutive Next() calls,
	// so that invalidation of the earlier one by Next() is detected.
	var partitions []types.Partition
	for i := 0; i < 2; i++ {
		p, err := m.Next()
		if err != nil {
			t.Fatalf("Next() call %d: unexpected error: %v", i, err)
		}
		partitions = append(partitions, p)
	}

	var wg sync.WaitGroup
	for _, p := range partitions {
		start := int64(p.GetStartSector()) * mbr.Sector
		want := disk[start : start+int64(p.GetSize())*mbr.Sector]
		for g := 0; g < 8; g++ {
			wg.Add(1)
			go func(p types.Partition, seed int64) {
				defer wg.Done()
				verifyRandomReadAt(t, p, want, seed)
			}(p, int64(g))
		}
		wg.Add(1)
		go func(p types.Partition) {
			defer wg.Done()
			sr := p.GetSectionReader()
			got, err := io.ReadAll(&sr)
			if err != nil {
				t.Errorf("partition %s: ReadAll() error: %v", p.Name(), err)
				return
			}
			if !bytes.Equal(got, want) {
				t.Errorf("partition %s: GetSectionReader() content mismatch", p.Name())
			}
		}(p)
	}
	wg.Wait()
}

func TestPartition_ReadAtBounds(t *testing.T) {
	disk := newPatternedMBRDisk()
	m, err := mbr.NewMasterBootRecord(io.NewSectionReader(newReaderAt(disk), 0, int64(len(disk))))
	if err != nil {
		t.Fatalf("NewMasterBootRecord() unexpected error: %v", err)
	}
	p, err := m.Next()
	if err != nil {
		t.Fatalf("Next() unexpected error: %v", err)
	}
	size := int64(p.GetSize()) * mbr.Sector
	start := int64(p.GetStartSector()) * mbr.Sector

	tests := []struct {
		name    string
		off     int64
		len     int
		wantN   int
		wantErr error
	}{
		{name: "inside", off: 0, len: 16, wantN: 16, wantErr: nil},
		{name: "up to the end", off: size - 16, len: 16, wantN: 16, wantErr: nil},
		{name: "across the end", off: size - 8, len: 16, wantN: 8, wantErr: io.EOF},
		{name: "at the end", off: size, len: 16, wantN: 0, wantErr: io.EOF},
		{name: "past the end", off: size + mbr.Sector, len: 16, wantN: 0, wantErr: io.EOF},
		{name: "negative offset", off: -1, len: 16, wantN: 0, wantErr: io.EOF},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			b := make([]byte, tt.len)
			n, err := p.ReadAt(b, tt.off)
			if n != tt.wantN || err != tt.wantErr {
				t.Fatalf("ReadAt(len=%d, off=%d) = (%d, %v), want (%d, %v)", tt.len, tt.off, n, err, tt.wantN, tt.wantErr)
			}
			if n > 0 && !bytes.Equal(b[:n], disk[start+tt.off:start+tt.off+int64(n)]) {
				t.Errorf("ReadAt(len=%d, off=%d) content mismatch", tt.len, tt.off)
			}
		})
	}
}

// verifyRandomReadAt reads p at random offsets, including reads across its end, and compares with want.
func verifyRandomReadAt(t *testing.T, p io.ReaderAt, want []byte, seed int64) {
	t.Helper()
	rnd := rand.New(rand.NewSource(seed))
	size := int64(len(want))
	for i := 0; i < 200; i++ {
		off := rnd.Int63n(size)
		b := make([]byte, 1+rnd.Intn(2*mbr.Sector))
		n, err := p.ReadAt(b, off)
		wantN := len(b)
		if rest := size - off; int64(wantN) > rest {
			wantN = int(rest)
			if err != io.EOF {
				t.Errorf("ReadAt(len=%d, off=%d): err = %v, want io.EOF", len(b), off, err)
				return
			}
		} else if err != nil {
			t.Errorf("ReadAt(len=%d, off=%d): unexpected error: %v", len(b), off, err)
			return
		}
		if n != wantN {
			t.Errorf("ReadAt(len=%d, off=%d): n = %d, want %d", len(b), off, n, wantN)
			return
		}
		if !bytes.Equal(b[:n], want[off:off+int64(n)]) {
			t.Errorf("ReadAt(len=%d, off=%d): content mismatch", len(b), off)
			return
		}
	}
}
