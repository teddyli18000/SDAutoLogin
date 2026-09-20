//go:build ignore

// icogen writes the multi size Windows icon from the same drawing code the
// running program uses, so the file in Explorer and the tray never differ.
//
//	go run tools/icogen/main.go winres/icon.ico
package main

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"

	"sdautologin/internal/brand"
)

// The sizes Explorer, the task bar and the alt-tab switcher ask for.
var sizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

func main() {
	out := "winres/icon.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	if dir := filepath.Dir(out); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			fail(err)
		}
	}

	images := make([][]byte, len(sizes))
	for i, size := range sizes {
		png := brand.PNG(size, brand.AccentBrand)
		if len(png) == 0 {
			fail(fmt.Errorf("encoding the %dpx image", size))
		}
		images[i] = png
	}

	if err := os.WriteFile(out, encodeICO(sizes, images), 0o644); err != nil {
		fail(err)
	}
	fmt.Printf("wrote %s: %d entries, %d bytes\n", out, len(images), sizeOf(out))
}

// encodeICO lays out an ICONDIR followed by ICONDIRENTRY records and the PNG
// payloads. PNG entries are accepted by every Windows since Vista.
func encodeICO(sizes []int, images [][]byte) []byte {
	const headerSize = 6
	const entrySize = 16

	buf := make([]byte, headerSize+entrySize*len(images))
	binary.LittleEndian.PutUint16(buf[0:], 0) // reserved
	binary.LittleEndian.PutUint16(buf[2:], 1) // type: icon
	binary.LittleEndian.PutUint16(buf[4:], uint16(len(images)))

	offset := len(buf)
	for i, img := range images {
		e := buf[headerSize+i*entrySize:]
		size := sizes[i]
		dim := byte(size)
		if size >= 256 {
			dim = 0 // 0 means 256 in the directory
		}
		e[0] = dim
		e[1] = dim
		e[2] = 0                                 // palette size, 0 for true colour
		e[3] = 0                                 // reserved
		binary.LittleEndian.PutUint16(e[4:], 1)  // colour planes
		binary.LittleEndian.PutUint16(e[6:], 32) // bits per pixel
		binary.LittleEndian.PutUint32(e[8:], uint32(len(img)))
		binary.LittleEndian.PutUint32(e[12:], uint32(offset))
		offset += len(img)
	}
	for _, img := range images {
		buf = append(buf, img...)
	}
	return buf
}

func sizeOf(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "icogen:", err)
	os.Exit(1)
}
