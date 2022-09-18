package main

import (
	"fmt"
	"github.com/masahiro331/go-disk"
	"io"
	"log"
	"os"
)

func main() {
	if len(os.Args) != 2 {
		log.Fatal("arguments error, './main ${file}'")
	}
	f, err := os.Open(os.Args[1])
	if err != nil {
		log.Fatal(err)
	}
	fi, err := f.Stat()
	if err != nil {
		log.Fatal(err)
	}
	r := io.NewSectionReader(f, 0, fi.Size())
	driver, err := disk.NewDriver(r)
	if err != nil {
		log.Fatal(err)
	}

	count := 0
	for {
		p, err := driver.Next()
		if err != nil {
			if err == io.EOF {
				return
			}
			log.Fatal(p.Name(), err)
		}
		f, err = os.Create(fmt.Sprintf("%s%d", p.Name(), count))
		if err != nil {
			log.Fatal(err)
		}
		r := p.GetSectionReader()
		io.Copy(f, &r)
		count++
	}
}
