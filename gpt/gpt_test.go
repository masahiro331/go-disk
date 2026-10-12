package gpt

import (
	"bytes"
	"encoding/binary"
	"io"
	"math"
	"math/rand"
	"sync"
	"testing"

	"github.com/masahiro331/go-disk/types"
)

func newSectionReaderWithMarkers(size int, markers map[int64]byte) *io.SectionReader {
	buf := make([]byte, size)
	for offset, b := range markers {
		buf[offset] = b
	}
	return io.NewSectionReader(bytes.NewReader(buf), 0, int64(size))
}

func TestGUIDPartitionTable_Next(t *testing.T) {
	tests := []struct {
		name    string
		entries []PartitionEntry
		// marker byte written at each entry's StartingLBA * 512
		markers []byte
	}{
		{
			name:    "empty entries",
			entries: []PartitionEntry{},
			markers: nil,
		},
		{
			name: "single entry with non-zero index",
			entries: []PartitionEntry{
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       2048,
					EndingLBA:         4095,
					index:             5,
				},
			},
			markers: []byte{0xAA},
		},
		{
			name: "contiguous indices",
			entries: []PartitionEntry{
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       2048,
					EndingLBA:         4095,
					index:             0,
				},
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       4096,
					EndingLBA:         6143,
					index:             1,
				},
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       6144,
					EndingLBA:         8191,
					index:             2,
				},
			},
			markers: []byte{0xAA, 0xBB, 0xCC},
		},
		{
			name: "non-contiguous indices",
			entries: []PartitionEntry{
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       2048,
					EndingLBA:         4095,
					index:             0,
				},
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       4096,
					EndingLBA:         6143,
					index:             13,
				},
				{
					PartitionTypeGUID: GUID{0x01},
					StartingLBA:       6144,
					EndingLBA:         8191,
					index:             14,
				},
			},
			markers: []byte{0xAA, 0xBB, 0xCC},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gpt := &GUIDPartitionTable{
				Entries:       tt.entries,
				sectionReader: newSectionReaderWithMarkers(8192*512, nil),
			}

			if len(tt.entries) == 0 {
				_, err := gpt.Next()
				if err != io.EOF {
					t.Errorf("Next() on empty entries: got %v, want io.EOF", err)
				}
				return
			}

			// Write marker bytes at each entry's starting offset
			markerMap := make(map[int64]byte)
			for i, e := range tt.entries {
				markerMap[int64(e.StartingLBA)*512] = tt.markers[i]
			}
			gpt.sectionReader = newSectionReaderWithMarkers(8192*512, markerMap)

			for i := range tt.entries {
				p, err := gpt.Next()
				if err != nil {
					t.Fatalf("Next() call %d: unexpected error: %v", i, err)
				}
				pe, ok := p.(*PartitionEntry)
				if !ok {
					t.Fatalf("Next() call %d: unexpected type %T", i, p)
				}
				if pe.Index() != tt.entries[i].index {
					t.Errorf("Next() call %d: got index %d, want %d", i, pe.Index(), tt.entries[i].index)
				}

				sr := pe.GetSectionReader()
				expectedSize := int64(pe.GetSize() * 512)
				if sr.Size() != expectedSize {
					t.Errorf("Next() call %d: SectionReader size = %d, want %d", i, sr.Size(), expectedSize)
				}

				// Verify SectionReader reads from the correct offset
				b := make([]byte, 1)
				if _, err := sr.Read(b); err != nil {
					t.Fatalf("Next() call %d: SectionReader.Read() error: %v", i, err)
				}
				if b[0] != tt.markers[i] {
					t.Errorf("Next() call %d: first byte = %x, want %x", i, b[0], tt.markers[i])
				}
			}

			_, err := gpt.Next()
			if err != io.EOF {
				t.Errorf("Next() after all entries: got %v, want io.EOF", err)
			}
		})
	}
}

// newPatternedGPTDisk builds a disk with a GPT header at LBA 1 and a partition entry array at LBA 2.
// The bytes after the entry array are a pattern of their absolute offset,
// so a read from the wrong position is detected.
//
// Partition layout:
//
//	entry[0] LBA 10-17
//	entry[1] LBA 18-29
func newPatternedGPTDisk() []byte {
	buf := make([]byte, 32*Sector)
	for i := 3 * Sector; i < len(buf); i++ {
		buf[i] = byte(i % 251)
	}

	header := buf[Sector : 2*Sector]
	copy(header[0:8], Signature)
	binary.LittleEndian.PutUint32(header[12:], 92)  // HeaderSize
	binary.LittleEndian.PutUint64(header[72:], 2)   // PartitionEntryLBA
	binary.LittleEndian.PutUint32(header[80:], 4)   // NumberOfPartitionEntries
	binary.LittleEndian.PutUint32(header[84:], 128) // SizeOfPartitionEntry

	entries := buf[2*Sector : 3*Sector]
	for i, lba := range [][2]uint64{{10, 17}, {18, 29}} {
		e := entries[i*PartitionEntrySize:]
		e[0] = 0x01 // PartitionTypeGUID, non-zero to mark the entry as used
		binary.LittleEndian.PutUint64(e[32:], lba[0])
		binary.LittleEndian.PutUint64(e[40:], lba[1])
	}
	return buf
}

func TestPartitionEntry_ConcurrentReadAt(t *testing.T) {
	disk := newPatternedGPTDisk()
	g, err := NewGUIDPartitionTable(bytes.NewReader(disk))
	if err != nil {
		t.Fatalf("NewGUIDPartitionTable() unexpected error: %v", err)
	}

	// Entries must be readable before Next() reaches them.
	b := make([]byte, 1)
	if _, err := g.Entries[1].ReadAt(b, 0); err != nil || b[0] != disk[18*Sector] {
		t.Fatalf("Entries[1].ReadAt() before Next() = (%x, %v), want (%x, nil)", b[0], err, disk[18*Sector])
	}

	// Hold two partitions returned by consecutive Next() calls,
	// so that invalidation of the earlier one by Next() is detected.
	var partitions []types.Partition
	for i := 0; i < 2; i++ {
		p, err := g.Next()
		if err != nil {
			t.Fatalf("Next() call %d: unexpected error: %v", i, err)
		}
		partitions = append(partitions, p)
	}
	if _, err := g.Next(); err != io.EOF {
		t.Fatalf("Next() after all entries: got %v, want io.EOF", err)
	}

	var wg sync.WaitGroup
	for _, p := range partitions {
		start := int64(p.GetStartSector()) * Sector
		want := disk[start : start+int64(p.GetSize())*Sector]
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func(p types.Partition, seed int64) {
				defer wg.Done()
				verifyRandomPartitionReadAt(t, p, want, seed)
			}(p, int64(i))
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

func TestPartitionEntry_ReadAtBounds(t *testing.T) {
	disk := newPatternedGPTDisk()
	g, err := NewGUIDPartitionTable(bytes.NewReader(disk))
	if err != nil {
		t.Fatalf("NewGUIDPartitionTable() unexpected error: %v", err)
	}
	p, err := g.Next()
	if err != nil {
		t.Fatalf("Next() unexpected error: %v", err)
	}
	size := int64(p.GetSize()) * Sector
	start := int64(p.GetStartSector()) * Sector

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
		{name: "past the end", off: size + Sector, len: 16, wantN: 0, wantErr: io.EOF},
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

// verifyRandomPartitionReadAt reads p at random offsets, including reads across its end, and compares with want.
func verifyRandomPartitionReadAt(t *testing.T, p io.ReaderAt, want []byte, seed int64) {
	t.Helper()
	rnd := rand.New(rand.NewSource(seed))
	size := int64(len(want))
	for i := 0; i < 200; i++ {
		off := rnd.Int63n(size)
		b := make([]byte, 1+rnd.Intn(2*Sector))
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

// newGPTDiskWithEntries builds a disk with a GPT header at LBA 1 and the given [StartingLBA, EndingLBA]
// entries at LBA 2. The bytes after the entry array are a pattern of their absolute offset.
func newGPTDiskWithEntries(entries [][2]uint64) []byte {
	buf := make([]byte, 32*Sector)
	for i := 3 * Sector; i < len(buf); i++ {
		buf[i] = byte(i % 251)
	}

	header := buf[Sector : 2*Sector]
	copy(header[0:8], Signature)
	binary.LittleEndian.PutUint32(header[12:], 92)  // HeaderSize
	binary.LittleEndian.PutUint64(header[72:], 2)   // PartitionEntryLBA
	binary.LittleEndian.PutUint32(header[80:], 4)   // NumberOfPartitionEntries
	binary.LittleEndian.PutUint32(header[84:], 128) // SizeOfPartitionEntry

	array := buf[2*Sector : 3*Sector]
	for i, lba := range entries {
		e := array[i*PartitionEntrySize:]
		e[0] = 0x01 // PartitionTypeGUID, non-zero to mark the entry as used
		binary.LittleEndian.PutUint64(e[32:], lba[0])
		binary.LittleEndian.PutUint64(e[40:], lba[1])
	}
	return buf
}

func TestPartitionEntry_InvalidRange(t *testing.T) {
	// The largest EndingLBA whose end offset, (EndingLBA+1)*Sector, fits in int64.
	const maxEndingLBA = math.MaxInt64/Sector - 1

	tests := []struct {
		name     string
		lba      [2]uint64
		wantSize uint64
	}{
		{name: "ending before starting", lba: [2]uint64{20, 12}, wantSize: 0},
		{name: "ending at the int64 limit", lba: [2]uint64{maxEndingLBA, maxEndingLBA}, wantSize: 1},
		{name: "ending past the int64 limit", lba: [2]uint64{maxEndingLBA, maxEndingLBA + 1}, wantSize: 0},
		{name: "starting past the int64 limit", lba: [2]uint64{math.MaxUint64 - 1, math.MaxUint64}, wantSize: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// entry[0] is a valid partition, which must stay readable next to the invalid entry[1].
			disk := newGPTDiskWithEntries([][2]uint64{{10, 17}, tt.lba})
			g, err := NewGUIDPartitionTable(bytes.NewReader(disk))
			if err != nil {
				t.Fatalf("NewGUIDPartitionTable() unexpected error: %v", err)
			}

			valid, invalid := g.Entries[0], g.Entries[1]
			if got := invalid.GetSize(); got != tt.wantSize {
				t.Errorf("GetSize() = %d, want %d", got, tt.wantSize)
			}
			sr := invalid.GetSectionReader()
			if got, want := sr.Size(), int64(tt.wantSize)*Sector; got != want {
				t.Errorf("GetSectionReader().Size() = %d, want %d", got, want)
			}
			// Every case lies beyond the 32-sector disk or is empty.
			if n, err := invalid.ReadAt(make([]byte, Sector), 0); n != 0 || err != io.EOF {
				t.Errorf("ReadAt() = (%d, %v), want (0, io.EOF)", n, err)
			}

			b := make([]byte, 8*Sector)
			if _, err := valid.ReadAt(b, 0); err != nil || !bytes.Equal(b, disk[10*Sector:18*Sector]) {
				t.Errorf("valid entry ReadAt() = %v, or content mismatch", err)
			}
		})
	}
}

// countingReaderAt counts the ReadAt calls, to check that nothing is read past the header.
type countingReaderAt struct {
	r     io.ReaderAt
	reads int
}

func (c *countingReaderAt) ReadAt(p []byte, off int64) (int, error) {
	c.reads++
	return c.r.ReadAt(p, off)
}

func TestNewGUIDPartitionTable_EntryArraySize(t *testing.T) {
	const maxEntries = (4 << 20) / PartitionEntrySize

	t.Run("over the limit", func(t *testing.T) {
		disk := newGPTDiskWithEntries(nil)
		binary.LittleEndian.PutUint32(disk[Sector+80:], maxEntries+1) // NumberOfPartitionEntries
		r := &countingReaderAt{r: bytes.NewReader(disk)}
		if _, err := NewGUIDPartitionTable(r); err == nil {
			t.Fatal("NewGUIDPartitionTable() expected error, got nil")
		}
		if r.reads != 1 {
			t.Errorf("ReadAt called %d times, want 1 (the header only)", r.reads)
		}
	})

	t.Run("at the limit", func(t *testing.T) {
		// The entry array is all zeros, so every entry is unused.
		disk := make([]byte, 2*Sector+4<<20)
		copy(disk, newGPTDiskWithEntries(nil)[:2*Sector])
		binary.LittleEndian.PutUint32(disk[Sector+80:], maxEntries) // NumberOfPartitionEntries
		g, err := NewGUIDPartitionTable(bytes.NewReader(disk))
		if err != nil {
			t.Fatalf("NewGUIDPartitionTable() unexpected error: %v", err)
		}
		if len(g.Entries) != 0 {
			t.Errorf("got %d entries, want 0", len(g.Entries))
		}
	})
}

func TestNewGUIDPartitionTable_PartitionEntryLBA(t *testing.T) {
	t.Run("array at LBA 3", func(t *testing.T) {
		disk := newGPTDiskWithEntries([][2]uint64{{10, 17}})
		copy(disk[3*Sector:4*Sector], disk[2*Sector:3*Sector])
		copy(disk[2*Sector:3*Sector], make([]byte, Sector))
		binary.LittleEndian.PutUint64(disk[Sector+72:], 3) // PartitionEntryLBA

		g, err := NewGUIDPartitionTable(bytes.NewReader(disk))
		if err != nil {
			t.Fatalf("NewGUIDPartitionTable() unexpected error: %v", err)
		}
		if len(g.Entries) != 1 || g.Entries[0].StartingLBA != 10 || g.Entries[0].EndingLBA != 17 {
			t.Errorf("Entries = %+v, want one entry at LBA 10-17", g.Entries)
		}
	})

	// The entry array of newGPTDiskWithEntries is one sector (4 entries).
	// 5 entries take 640 bytes, rounded up to a two-sector array.
	maxLBA := uint64(math.MaxInt64-Sector) / Sector
	maxLBA2 := uint64(math.MaxInt64-2*Sector) / Sector
	tests := []struct {
		name       string
		lba        uint64
		numEntries uint32
		wantReads  int
	}{
		{name: "LBA 0", lba: 0, numEntries: 4, wantReads: 1},
		{name: "LBA 1", lba: 1, numEntries: 4, wantReads: 1},
		// Within int64, so the array is read and the read fails past the end of the disk.
		{name: "end at the int64 limit", lba: maxLBA, numEntries: 4, wantReads: 2},
		{name: "end past the int64 limit", lba: maxLBA + 1, numEntries: 4, wantReads: 1},
		{name: "two-sector array end at the int64 limit", lba: maxLBA2, numEntries: 5, wantReads: 2},
		{name: "two-sector array end past the int64 limit", lba: maxLBA2 + 1, numEntries: 5, wantReads: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			disk := newGPTDiskWithEntries(nil)
			binary.LittleEndian.PutUint64(disk[Sector+72:], tt.lba)        // PartitionEntryLBA
			binary.LittleEndian.PutUint32(disk[Sector+80:], tt.numEntries) // NumberOfPartitionEntries
			r := &countingReaderAt{r: bytes.NewReader(disk)}
			if _, err := NewGUIDPartitionTable(r); err == nil {
				t.Fatal("NewGUIDPartitionTable() expected error, got nil")
			}
			if r.reads != tt.wantReads {
				t.Errorf("ReadAt called %d times, want %d", r.reads, tt.wantReads)
			}
		})
	}
}
