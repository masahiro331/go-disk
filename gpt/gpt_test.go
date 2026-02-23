package gpt

import (
	"bytes"
	"io"
	"testing"
)

func newSectionReader(size int) *io.SectionReader {
	buf := make([]byte, size)
	return io.NewSectionReader(bytes.NewReader(buf), 0, int64(size))
}

func TestGUIDPartitionTable_Next(t *testing.T) {
	tests := []struct {
		name            string
		entries         []PartitionEntry
		expectedIndices []int
	}{
		{
			name:            "empty entries",
			entries:         []PartitionEntry{},
			expectedIndices: nil,
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
			expectedIndices: []int{5},
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
			expectedIndices: []int{0, 1, 2},
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
			expectedIndices: []int{0, 13, 14},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gpt := &GUIDPartitionTable{
				Entries:       tt.entries,
				sectionReader: newSectionReader(8192 * 512),
			}

			for i, want := range tt.expectedIndices {
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
				sr := pe.GetSectionReader()
				expectedSize := int64(pe.GetSize() * 512)
				if sr.Size() != expectedSize {
					t.Errorf("Next() call %d: SectionReader size = %d, want %d", i, sr.Size(), expectedSize)
				}
			}

			_, err := gpt.Next()
			if err != io.EOF {
				t.Errorf("Next() after all entries: got %v, want io.EOF", err)
			}
		})
	}
}
