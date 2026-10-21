package disk_test

import (
	"bytes"
	"encoding/binary"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/masahiro331/go-disk"
	"github.com/masahiro331/go-disk/fs"
	"github.com/masahiro331/go-disk/gpt"
	"github.com/masahiro331/go-disk/mbr"
	"github.com/masahiro331/go-xfs-filesystem/xfs"
)

func TestNewDriver(t *testing.T) {
	tests := []struct {
		name         string
		inputFile    string
		wantErr      string
		checkFsFuncs []fs.CheckFsFunc
	}{
		{
			name:         "Happy path, Direct filesystem",
			inputFile:    "testdata/fs.bin",
			wantErr:      "",
			checkFsFuncs: []fs.CheckFsFunc{xfs.Check},
		},
		{
			name:         "Invalid path, Direct filesystem no check fs functions",
			inputFile:    "testdata/fs.bin",
			wantErr:      "Invalid master boot record signature",
			checkFsFuncs: []fs.CheckFsFunc{},
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
			sr := io.NewSectionReader(f, 0, info.Size())
			_, err = disk.NewDriver(sr, tt.checkFsFuncs...)

			if tt.wantErr == "" && err != nil {
				t.Errorf("input: %s, required no error = %v", tt.inputFile, err)
			} else if tt.wantErr != "" && !strings.HasSuffix(err.Error(), tt.wantErr) {
				t.Errorf("input: %s, expected: %q, actual: %q", tt.inputFile, tt.wantErr, err.Error())
			}
		})
	}
}

// newDisk builds a disk whose MBR has the given disk signature and entry types.
// A 0xEE entry covers the disk from LBA 1, as in a protective MBR, and other non-empty entries are at sector 40.
// If withGPT is true, a GPT header at LBA 1 has one used entry at sector 34.
func newDisk(signature uint32, entryTypes [4]byte, withGPT bool) []byte {
	const sectors = 64
	buf := make([]byte, sectors*mbr.Sector)
	binary.LittleEndian.PutUint32(buf[440:], signature)
	for i, typ := range entryTypes {
		e := buf[446+16*i:]
		e[4] = typ
		switch typ {
		case 0:
		case 0xEE:
			binary.LittleEndian.PutUint32(e[8:], 1)
			binary.LittleEndian.PutUint32(e[12:], sectors-1)
		default:
			binary.LittleEndian.PutUint32(e[8:], 40)
			binary.LittleEndian.PutUint32(e[12:], 8)
		}
	}
	binary.LittleEndian.PutUint16(buf[510:], mbr.SIGNATURE)

	if withGPT {
		header := buf[gpt.Sector:]
		copy(header[0:8], gpt.Signature)
		binary.LittleEndian.PutUint32(header[12:], 92)  // HeaderSize
		binary.LittleEndian.PutUint64(header[72:], 2)   // PartitionEntryLBA
		binary.LittleEndian.PutUint32(header[80:], 4)   // NumberOfPartitionEntries
		binary.LittleEndian.PutUint32(header[84:], 128) // SizeOfPartitionEntry
		entry := buf[2*gpt.Sector:]
		entry[0] = 0x01 // PartitionTypeGUID, non-zero to mark the entry as used
		binary.LittleEndian.PutUint64(entry[32:], 34)
		binary.LittleEndian.PutUint64(entry[40:], 39)
	}
	return buf
}

func TestNewDriver_PartitionTableType(t *testing.T) {
	tests := []struct {
		name       string
		signature  uint32
		entryTypes [4]byte
		withGPT    bool
		wantGPT    bool
		wantErr    string
	}{
		{name: "MBR with a zero signature", entryTypes: [4]byte{0x83}},
		{name: "MBR with a signature", signature: 0x12345678, entryTypes: [4]byte{0x83}},
		{name: "protective MBR with a zero signature", entryTypes: [4]byte{0xEE}, withGPT: true, wantGPT: true},
		{name: "protective MBR with a signature", signature: 0x12345678, entryTypes: [4]byte{0xEE}, withGPT: true, wantGPT: true},
		{name: "protective MBR without a GPT", signature: 0x12345678, entryTypes: [4]byte{0xEE}, wantErr: "invalid GPT signature"},
		{name: "hybrid MBR", entryTypes: [4]byte{0x83, 0, 0, 0xEE}, withGPT: true, wantGPT: true},
		// Like the Arch and Alpine ISOs, which Linux reads as MBR.
		{name: "GPT header without a protective entry", signature: 0x12345678, entryTypes: [4]byte{0, 0xEF}, withGPT: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := disk.NewDriver(bytes.NewReader(newDisk(tt.signature, tt.entryTypes, tt.withGPT)))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("NewDriver() = (%T, %v), want error containing %q", d, err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("NewDriver() unexpected error: %v", err)
			}

			_, isGPT := d.(*gpt.GUIDPartitionTable)
			if isGPT != tt.wantGPT {
				t.Fatalf("NewDriver() = %T, want GPT: %v", d, tt.wantGPT)
			}
			wantStart := uint64(40)
			if tt.wantGPT {
				wantStart = 34
			}
			for {
				p, err := d.Next()
				if err != nil {
					t.Fatalf("Next() found no partition: %v", err)
				}
				if p.GetSize() == 0 {
					continue
				}
				if got := p.GetStartSector(); got != wantStart {
					t.Errorf("first partition start = %d, want %d", got, wantStart)
				}
				return
			}
		})
	}
}
