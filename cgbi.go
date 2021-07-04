package cgbi

import (
	"encoding/binary"
	"image"
)

// chunkDataCgBI why ?
// BigEndian 1342185478 => 2012-07-13 21:17:58
// LittleEndian 102760528 => 1973-04-04 16:35:28
var (
	chunkDataCgBIValue = uint32(1342185478)
	chunkDataCgBI      = make([]byte, 4)
)

func init() {
	binary.BigEndian.PutUint32(chunkDataCgBI, chunkDataCgBIValue)
	image.RegisterFormat("cgbi", pngHeader, Decode, DecodeConfig)
}
