package cgbi

import (
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Color type, as per the PNG spec.
const (
	ctGrayscale      = 0
	ctTrueColor      = 2
	ctPaletted       = 3
	ctGrayscaleAlpha = 4
	ctTrueColorAlpha = 6
)

// A cb is a combination of color type and bit depth.
const (
	cbInvalid = iota
	cbG1
	cbG2
	cbG4
	cbG8
	cbGA8
	cbTC8
	cbP1
	cbP2
	cbP4
	cbP8
	cbTCA8
	cbG16
	cbGA16
	cbTC16
	cbTCA16
)

// Interlace type.
const (
	itNone  = 0
	itAdam7 = 1
)

// interlaceScan defines the placement and size of a pass for Adam7 interlacing.
type interlaceScan struct {
	xFactor, yFactor, xOffset, yOffset int
}

// interlacing defines Adam7 interlacing, with 7 passes of reduced images.
// See https://www.w3.org/TR/PNG/#8Interlace
var interlacing = []interlaceScan{
	{8, 8, 0, 0},
	{8, 8, 4, 0},
	{4, 8, 0, 4},
	{4, 4, 2, 0},
	{2, 4, 0, 2},
	{2, 2, 1, 0},
	{1, 2, 0, 1},
}

func makeChunk(w io.Writer, typ, data []byte) error {
	chunkLength := uint32(len(data))
	// write length
	if err := binary.Write(w, binary.BigEndian, &chunkLength); err != nil {
		return err
	}
	// write chunkType
	if _, err := w.Write(typ); err != nil {
		return err
	}
	// write chunkData
	if _, err := w.Write(data); err != nil {
		return err
	}
	crc := crc32.NewIEEE()
	crc.Write(typ)
	crc.Write(data)
	chunkCRC := crc.Sum32()
	crc.Reset()
	// write chunkCRC
	if err := binary.Write(w, binary.BigEndian, &chunkCRC); err != nil {
		return err
	}
	return nil
}

// unsafeImageFix Swapping red & blue bytes for each pixel
func unsafeImageFix(w, h int, raw []byte) {
	i := 0
	for y := 0; y < h; y++ {
		i++
		for x := 0; x < w; x++ {
			raw[i+2], raw[i+0] = raw[i+0], raw[i+2]
			i += 4
		}
	}
}

func rawImageFix(w, h, interlace int, raw []byte) error {
	if interlace == itNone {
		unsafeImageFix(w, h, raw)
		return nil
	}
	total := 0
	for pass := 0; pass < 7; pass++ {
		p := interlacing[pass]
		wp := (w - p.xOffset + p.xFactor - 1) / p.xFactor
		hp := (h - p.yOffset + p.yFactor - 1) / p.yFactor
		pSize := wp*hp*4 + hp
		if total+pSize > len(raw) {
			return UnexpectedError("amount of image data")
		}
		unsafeImageFix(wp, hp, raw[total:])
		total = total + pSize
	}
	return nil
}
