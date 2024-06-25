package disk_test

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/masahiro331/go-disk"
	"github.com/masahiro331/go-disk/fs"
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
