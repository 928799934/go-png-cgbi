package cgbi

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"hash"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"io/ioutil"
)

const (
	dsStart = iota
	dsSeenCgBI
	dsSeenIHDR
	dsSeenIDAT
	dsSeenIEND
)

const pngHeader = "\x89PNG\r\n\x1a\n"

type decoder struct {
	r         io.Reader
	buff      *bytes.Buffer
	zbuff     *bytes.Buffer
	crc       hash.Hash32
	width     int
	height    int
	interlace int
	stage     int
	cb        int
	tmp       [3 * 256]byte
}

// A FormatError reports that the input is not a valid PNG.
type FormatError string

func (e FormatError) Error() string { return "png: invalid format: " + string(e) }

var chunkOrderError = FormatError("chunk out of order")

// An UnsupportedError reports that the input uses a valid but unimplemented PNG feature.
type UnsupportedError string

func (e UnsupportedError) Error() string { return "png: unsupported feature: " + string(e) }

// An UnexpectedError reports that the input uses a valid but unimplemented PNG feature.
type UnexpectedError string

func (e UnexpectedError) Error() string { return "png: unexpected error: " + string(e) }

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (d *decoder) checkHeader() error {
	_, err := io.ReadFull(d.r, d.tmp[:len(pngHeader)])
	if err != nil {
		return err
	}
	if string(d.tmp[:len(pngHeader)]) != pngHeader {
		return FormatError("not a PNG file")
	}
	d.buff.Write(d.tmp[:len(pngHeader)])
	return nil
}

func (d *decoder) parseCgBI(length uint32) error {
	if length != 4 {
		return FormatError("bad CgBI length")
	}
	if _, err := io.ReadFull(d.r, d.tmp[:4]); err != nil {
		return err
	}
	d.crc.Write(d.tmp[:4])
	if chunkDataCgBIValue != binary.BigEndian.Uint32(d.tmp[:4]) {
		return FormatError("bad CgBI data")
	}
	return d.verifyChecksum(false)
}

func (d *decoder) parseIHDR(length uint32) error {
	if length != 13 {
		return FormatError("bad IHDR length")
	}
	// write length + chunkType
	d.buff.Write(d.tmp[:8])
	if _, err := io.ReadFull(d.r, d.tmp[:13]); err != nil {
		return err
	}
	if d.tmp[12] != itNone && d.tmp[12] != itAdam7 {
		return FormatError("invalid interlace method")
	}

	if d.tmp[8] == 8 && d.tmp[9] == ctTrueColorAlpha {
		d.cb = cbTCA8
	}

	d.interlace = int(d.tmp[12])
	d.width = int(binary.BigEndian.Uint32(d.tmp[:4]))
	d.height = int(binary.BigEndian.Uint32(d.tmp[4:8]))

	d.crc.Write(d.tmp[:13])
	// write chunkData
	d.buff.Write(d.tmp[:13])
	return d.verifyChecksum(true)
}

func (d *decoder) parseIDAT(length uint32) error {
	if length > 0x7fffffff {
		return FormatError(fmt.Sprintf("Bad chunk length: %d", length))
	}
	// Ignore this chunk (of a known length).
	var ignored [4096]byte
	for length > 0 {
		n, err := io.ReadFull(d.r, ignored[:min(len(ignored), int(length))])
		if err != nil {
			return err
		}
		d.crc.Write(ignored[:n])
		d.zbuff.Write(ignored[:n])
		length -= uint32(n)
	}
	return d.verifyChecksum(false)
}

func (d *decoder) parseIEND(length uint32) error {
	if length != 0 {
		return FormatError("bad IEND length")
	}

	// Recode zip fix zlib.ErrChecksum
	// don't know CRC, will get zlib.ErrChecksum
	d.zbuff.Write([]byte{0, 0, 0, 0})
	r, err := zlib.NewReader(d.zbuff)
	if err != nil {
		return err
	}
	data, err := ioutil.ReadAll(r)
	if err != zlib.ErrChecksum {
		_ = r.Close()
		return err
	}
	_ = r.Close()

	if err = rawImageFix(d.width, d.height, d.interlace, data); err != nil {
		return err
	}

	d.zbuff.Reset()

	w := zlib.NewWriter(d.zbuff)
	if _, err = w.Write(data); err != nil {
		return err
	}
	_ = w.Close()

	if err = makeChunk(d.buff, []byte("IDAT"), d.zbuff.Bytes()); err != nil {
		return err
	}

	// write length + chunkType
	d.buff.Write(d.tmp[0:8])
	return d.verifyChecksum(true)
}

func (d *decoder) parseChunk() error {
	if _, err := io.ReadFull(d.r, d.tmp[:8]); err != nil {
		return err
	}
	length := binary.BigEndian.Uint32(d.tmp[:4])
	d.crc.Reset()
	d.crc.Write(d.tmp[4:8])

	// Read the chunk data.
	switch string(d.tmp[4:8]) {
	case "CgBI":
		if d.stage != dsStart {
			return chunkOrderError
		}
		d.stage = dsSeenCgBI
		return d.parseCgBI(length)
	case "IHDR":
		if d.stage != dsSeenCgBI {
			return chunkOrderError
		}
		d.stage = dsSeenIHDR
		if err := d.parseIHDR(length); err != nil {
			return err
		}
		if d.cb != cbTCA8 {
			return FormatError("cb must cbTCA8 ")
		}
		return nil
	case "IDAT":
		if d.stage < dsSeenIHDR || d.stage > dsSeenIDAT {
			return chunkOrderError
		}
		d.stage = dsSeenIDAT
		return d.parseIDAT(length)
	case "IEND":
		if d.stage != dsSeenIDAT {
			return chunkOrderError
		}
		d.stage = dsSeenIEND
		return d.parseIEND(length)
	}
	if length > 0x7fffffff {
		return FormatError(fmt.Sprintf("Bad chunk length: %d", length))
	}
	// write length + chunkType
	d.buff.Write(d.tmp[0:8])
	// Ignore this chunk (of a known length).
	var ignored [4096]byte
	for length > 0 {
		n, err := io.ReadFull(d.r, ignored[:min(len(ignored), int(length))])
		if err != nil {
			return err
		}
		d.crc.Write(ignored[:n])
		// write chunkData
		d.buff.Write(ignored[:n])
		length -= uint32(n)
	}
	return d.verifyChecksum(true)
}

func (d *decoder) verifyChecksum(bWriteCRC bool) error {
	if _, err := io.ReadFull(d.r, d.tmp[:4]); err != nil {
		return err
	}
	if binary.BigEndian.Uint32(d.tmp[:4]) != d.crc.Sum32() {
		return FormatError("invalid checksum")
	}
	if !bWriteCRC {
		return nil
	}
	d.buff.Write(d.tmp[:4])
	return nil
}

func decode(r io.Reader) (*decoder, error) {
	d := &decoder{
		r:     r,
		buff:  bytes.NewBuffer([]byte{}),
		zbuff: bytes.NewBuffer([]byte{0x78, 0x1}),
		crc:   crc32.NewIEEE(),
	}
	if err := d.checkHeader(); err != nil {
		if err == io.EOF {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	for d.stage != dsSeenIEND {
		if err := d.parseChunk(); err != nil {
			if err == io.EOF {
				err = io.ErrUnexpectedEOF
			}
			return nil, err
		}
	}
	return d, nil
}

// Decode reads a PNG image from r and returns it as an image.Image.
// The type of Image returned depends on the PNG contents.
func Decode(r io.Reader) (image.Image, error) {
	d, err := decode(r)
	if err != nil {
		return nil, err
	}
	return png.Decode(d.buff)
}

// DecodeConfig returns the color model and dimensions of a PNG image without
// decoding the entire image.
func DecodeConfig(r io.Reader) (image.Config, error) {
	d, err := decode(r)
	if err != nil {
		return image.Config{}, err
	}
	return png.DecodeConfig(d.buff)
}
