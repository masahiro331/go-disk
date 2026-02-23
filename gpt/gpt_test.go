package gpt

import (
	"io"
	"testing"
)

func TestGUIDPartitionTable_Next_NonContiguousIndex(t *testing.T) {
	// Create a buffer large enough to cover the LBA ranges used by test entries.
	// Entry 0: LBA 2048-4095, Entry 13: LBA 4096-6143, Entry 14: LBA 6144-8191
	// Max offset = 8192 * 512 = 4194304
	buf := make([]byte, 8192*512)
	sr := io.NewSectionReader(newReaderAt(buf), 0, int64(len(buf)))

	// Simulate non-contiguous GPT table indices (0, 13, 14)
	// that have been filtered to Entries slice of length 3.
	gpt := &GUIDPartitionTable{
		Entries: []PartitionEntry{
			{
				PartitionTypeGUID: GUID{0x01}, // non-zero = used
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
		sectionReader: sr,
	}

	expectedIndices := []int{0, 13, 14}
	for i, want := range expectedIndices {
		p, err := gpt.Next()
		if err != nil {
			t.Fatalf("Next() call %d: unexpected error: %v", i, err)
		}
		pe, ok := p.(*PartitionEntry)
		if !ok {
			t.Fatalf("Next() call %d: unexpected type %T", i, p)
		}
		if pe.Index() != want {
			t.Errorf("Next() call %d: got index %d, want %d", i, pe.Index(), want)
		}
	}

	// The next call should return io.EOF
	_, err := gpt.Next()
	if err != io.EOF {
		t.Errorf("Next() after all entries: got %v, want io.EOF", err)
	}
}

// readerAt wraps a byte slice to implement io.ReaderAt.
type readerAt struct {
	data []byte
}

func newReaderAt(data []byte) *readerAt {
	return &readerAt{data: data}
}

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
