package gpt

import (
	"bytes"
	"io"
	"testing"
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
