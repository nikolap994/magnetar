// Command mkicon builds multi-resolution icons from assets/icon.png:
// a Windows .ico (PNG-compressed entries) and PNGs for Linux icon themes.
//
//	go run ./tools/mkicon
package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"log"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/image/draw"
)

func main() {
	f, err := os.Open("assets/icon.png")
	if err != nil {
		log.Fatal(err)
	}
	src, err := png.Decode(f)
	f.Close()
	if err != nil {
		log.Fatal(err)
	}

	sizes := []int{16, 24, 32, 48, 64, 128, 256}
	var images [][]byte
	for _, s := range sizes {
		dst := image.NewNRGBA(image.Rect(0, 0, s, s))
		draw.CatmullRom.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Over, nil)
		var buf bytes.Buffer
		if err := png.Encode(&buf, dst); err != nil {
			log.Fatal(err)
		}
		images = append(images, buf.Bytes())
		dir := filepath.Join("packaging", "linux", "icons", "hicolor", itoa(s)+"x"+itoa(s), "apps")
		must(os.MkdirAll(dir, 0o755))
		must(os.WriteFile(filepath.Join(dir, "magnetar.png"), buf.Bytes(), 0o644))
	}

	// ICO container: ICONDIR + ICONDIRENTRY[] + PNG payloads.
	var ico bytes.Buffer
	binary.Write(&ico, binary.LittleEndian, [3]uint16{0, 1, uint16(len(sizes))})
	offset := 6 + 16*len(sizes)
	for i, s := range sizes {
		w := uint8(s)
		if s == 256 {
			w = 0
		}
		binary.Write(&ico, binary.LittleEndian, struct {
			W, H, Colors, Reserved uint8
			Planes, BPP            uint16
			Size, Offset           uint32
		}{w, w, 0, 0, 1, 32, uint32(len(images[i])), uint32(offset)})
		offset += len(images[i])
	}
	for _, b := range images {
		ico.Write(b)
	}
	must(os.WriteFile("assets/icon.ico", ico.Bytes(), 0o644))
	must(os.WriteFile("internal/desktop/icon.ico", ico.Bytes(), 0o644))
	must(os.WriteFile("internal/desktop/icon.png", images[3], 0o644)) // 48px for tray
	log.Println("icons written")
}

func itoa(n int) string {
	return strconv.Itoa(n)
}

func must(err error) {
	if err != nil {
		log.Fatal(err)
	}
}
