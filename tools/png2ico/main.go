package main

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/png"
	"os"

	xdraw "golang.org/x/image/draw"
)

type iconDirEntry struct {
	Width       byte
	Height      byte
	ColorCount  byte
	Reserved    byte
	Planes      uint16
	BitCount    uint16
	BytesInRes  uint32
	ImageOffset uint32
}

func main() {
	if len(os.Args) != 3 {
		panic("usage: png2ico input.png output.ico")
	}

	input, err := os.Open(os.Args[1])
	if err != nil {
		panic(err)
	}
	defer input.Close()

	source, err := png.Decode(input)
	if err != nil {
		panic(err)
	}

	sizes := []int{256, 128, 64, 48, 32, 16}
	images := make([][]byte, 0, len(sizes))
	for _, size := range sizes {
		dst := image.NewRGBA(image.Rect(0, 0, size, size))
		xdraw.CatmullRom.Scale(dst, dst.Bounds(), source, source.Bounds(), xdraw.Over, nil)

		var buf bytes.Buffer
		if err := png.Encode(&buf, dst); err != nil {
			panic(err)
		}
		images = append(images, buf.Bytes())
	}

	output, err := os.Create(os.Args[2])
	if err != nil {
		panic(err)
	}
	defer output.Close()

	must(binary.Write(output, binary.LittleEndian, uint16(0)))
	must(binary.Write(output, binary.LittleEndian, uint16(1)))
	must(binary.Write(output, binary.LittleEndian, uint16(len(images))))

	offset := uint32(6 + 16*len(images))
	entries := make([]iconDirEntry, len(images))
	for i, img := range images {
		width := byte(sizes[i])
		height := byte(sizes[i])
		if sizes[i] == 256 {
			width = 0
			height = 0
		}
		entries[i] = iconDirEntry{
			Width:       width,
			Height:      height,
			Planes:      1,
			BitCount:    32,
			BytesInRes:  uint32(len(img)),
			ImageOffset: offset,
		}
		offset += uint32(len(img))
	}

	for _, entry := range entries {
		must(binary.Write(output, binary.LittleEndian, entry))
	}
	for _, img := range images {
		_, err := output.Write(img)
		must(err)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
