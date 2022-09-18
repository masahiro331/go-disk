package mbr_test

import (
	"github.com/masahiro331/go-disk/mbr"
	"io"
	"os"
	"testing"
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
