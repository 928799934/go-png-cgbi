package cgbi

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"image"
	"image/color"
	"image/png"
	"io"
	"io/ioutil"
)

type encoder struct {
	w         io.Writer
	buff      *bytes.Buffer
	zbuff     *bytes.Buffer
	crc       hash.Hash32
	tmp       [4 * 256]byte
	cb        int
	width     int
	height    int
	interlace int
	stage     int
}

func (e *encoder) makeHeader() error {
	if _, err := e.w.Write([]byte(pngHeader)); err != nil {
		return err
	}
	// disuse header
	if _, err := io.ReadFull(e.buff, e.tmp[:len(pngHeader)]); err != nil {
		return err
	}
	return nil
}

func (e *encoder) makeCgBI() error {
	return makeChunk(e.w, []byte("CgBI"), chunkDataCgBI)
}

func (e *encoder) makeIHDR(length uint32) error {
	if length != 13 {
		return FormatError("bad IHDR length")
	}
	// write length + chunkType
	e.w.Write(e.tmp[:8])
	if _, err := io.ReadFull(e.buff, e.tmp[:13]); err != nil {
		return err
	}
	if e.tmp[12] != itNone && e.tmp[12] != itAdam7 {
		return FormatError("invalid interlace method")
	}
	e.interlace = int(e.tmp[12])
	e.width = int(binary.BigEndian.Uint32(e.tmp[:4]))
	e.height = int(binary.BigEndian.Uint32(e.tmp[4:8]))

	if e.tmp[8] == 8 && e.tmp[9] == ctTrueColorAlpha {
		e.cb = cbTCA8
	}

	// write chunkData
	e.w.Write(e.tmp[:13])
	return e.writeChecksum()
}

func (e *encoder) makeIDAT(length uint32) error {
	if length > 0x7fffffff {
		return FormatError(fmt.Sprintf("Bad chunk length: %d", length))
	}
	// Ignore this chunk (of a known length).
	var ignored [4096]byte
	for length > 0 {
		n, err := io.ReadFull(e.buff, ignored[:min(len(ignored), int(length))])
		if err != nil {
			return err
		}
		e.zbuff.Write(ignored[:n])
		length -= uint32(n)
	}
	// disuse crc sum
	if _, err := io.ReadFull(e.buff, e.tmp[:4]); err != nil {
		return err
	}
	return nil
}

func (e *encoder) makeIEND(length uint32) error {
	if length != 0 {
		return FormatError("bad IEND length")
	}
	r, err := zlib.NewReader(e.zbuff)
	if err != nil {
		return err
	}
	data, err := ioutil.ReadAll(r)
	if err != nil {
		_ = r.Close()
		return err
	}
	_ = r.Close()

	if err = rawImageFix(e.width, e.height, e.interlace, data); err != nil {
		return err
	}

	e.zbuff.Reset()
	w := zlib.NewWriter(e.zbuff)
	if _, err = w.Write(data); err != nil {
		return err
	}
	_ = w.Close()
	data = e.zbuff.Bytes()
	data = data[2:]
	data = data[:len(data)-4]
	if err = makeChunk(e.w, []byte("IDAT"), data); err != nil {
		return err
	}
	e.w.Write(e.tmp[0:8])
	return e.writeChecksum()
}

func (e *encoder) parseChunk() error {
	if _, err := io.ReadFull(e.buff, e.tmp[:8]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(e.tmp[:4])
	// Read the chunk data.
	switch string(e.tmp[4:8]) {
	case "IHDR":
		if e.stage != dsStart {
			return chunkOrderError
		}
		e.stage = dsSeenIHDR
		if err := e.makeIHDR(length); err != nil {
			return err
		}
		if e.cb != cbTCA8 {
			return FormatError("cb must cbTCA8 ")
		}
		return nil
	case "IDAT":
		if e.stage < dsSeenIHDR || e.stage > dsSeenIDAT {
			return chunkOrderError
		}
		e.stage = dsSeenIDAT
		return e.makeIDAT(length)
	case "IEND":
		if e.stage != dsSeenIDAT {
			return chunkOrderError
		}
		e.stage = dsSeenIEND
		return e.makeIEND(length)
	}
	if length > 0x7fffffff {
		return FormatError(fmt.Sprintf("Bad 1 chunk length: %d", length))
	}
	// write length + chunkType
	e.w.Write(e.tmp[0:8])
	// Ignore this chunk (of a known length).
	var ignored [4096]byte
	for length > 0 {
		n, err := io.ReadFull(e.buff, ignored[:min(len(ignored), int(length))])
		if err != nil {
			return err
		}
		// write chunkData
		e.w.Write(ignored[:n])
		length -= uint32(n)
	}
	return e.writeChecksum()
}

func (e *encoder) writeChecksum() error {
	if _, err := io.ReadFull(e.buff, e.tmp[:4]); err != nil {
		return err
	}
	e.w.Write(e.tmp[:4])
	return nil
}

func fixAlphaChannel(m image.Image) image.Image {
	bounds := m.Bounds()
	switch inst := m.(type) {
	case *image.NRGBA:
		for i, dx := 0, bounds.Dx(); i < dx; i++ {
			for j, dy := 0, bounds.Dy(); j < dy; j++ {
				r, g, b, _ := m.At(i, j).RGBA()
				inst.SetNRGBA(i, j, color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0x0000})
			}
		}
	default:
		nrgba := image.NewNRGBA(bounds)
		for i, dx := 0, bounds.Dx(); i < dx; i++ {
			for j, dy := 0, bounds.Dy(); j < dy; j++ {
				r, g, b, _ := m.At(i, j).RGBA()
				nrgba.SetNRGBA(i, j, color.NRGBA{R: uint8(r), G: uint8(g), B: uint8(b), A: 0x0000})
			}
		}
		m = nrgba
	}
	return m
}

// Encode writes the Image m to w in PNG format. Any Image may be
// encoded, but images that are not image.NRGBA might be encoded lossily.
func Encode(w io.Writer, m image.Image) error {
	e := &encoder{
		w:     w,
		buff:  bytes.NewBuffer([]byte{}),
		zbuff: bytes.NewBuffer([]byte{}),
		crc:   crc32.NewIEEE(),
	}

	if err := png.Encode(e.buff, fixAlphaChannel(m)); err != nil {
		return err
	}

	if err := e.makeHeader(); err != nil {
		return err
	}

	if err := e.makeCgBI(); err != nil {
		return err
	}

	for e.stage != dsSeenIEND {
		if err := e.parseChunk(); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return err
		}
	}
	return nil
}
