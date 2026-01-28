package pcm

import "encoding/binary"

// DownmixStereoPCM16LEToMono converts interleaved stereo PCM16LE (L,R) into mono PCM16LE.
// It averages L and R for each sample.
// Returns bytes written to dst.
func DownmixStereoPCM16LEToMono(dst []byte, src []byte) int {
	// Need 4 bytes per stereo sample (L16 + R16).
	nPairs := len(src) / 4
	need := nPairs * 2
	if len(dst) < need {
		nPairs = len(dst) / 2
	}
	for i := 0; i < nPairs; i++ {
		off := i * 4
		l := int16(binary.LittleEndian.Uint16(src[off : off+2]))
		r := int16(binary.LittleEndian.Uint16(src[off+2 : off+4]))
		m := int16((int32(l) + int32(r)) / 2)
		binary.LittleEndian.PutUint16(dst[i*2:i*2+2], uint16(m))
	}
	return nPairs * 2
}

// UpmixMonoPCM16LEToStereo converts mono PCM16LE into interleaved stereo PCM16LE by duplication (L=R=mono).
// Returns bytes written to dst.
func UpmixMonoPCM16LEToStereo(dst []byte, src []byte) int {
	n := len(src) / 2
	need := n * 4
	if len(dst) < need {
		n = len(dst) / 4
	}
	for i := 0; i < n; i++ {
		s := binary.LittleEndian.Uint16(src[i*2 : i*2+2])
		off := i * 4
		binary.LittleEndian.PutUint16(dst[off:off+2], s)
		binary.LittleEndian.PutUint16(dst[off+2:off+4], s)
	}
	return n * 4
}
