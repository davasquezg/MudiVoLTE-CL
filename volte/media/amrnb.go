package media

// AMR (narrowband, 8 kHz) in the RFC 4867 payload format. The layout is the
// one of AMR-WB (amrwb.go); only the frame types and their sizes differ.

// AMR frame types (3GPP TS 26.101 Table 1a). 0–7 are the speech modes
// 4.75 … 12.2 kbit/s, 8 is SID; 9–11 are the SIDs of other systems and
// 12–14 are for future use, which RFC 4867 §4.3.2 says to discard. 15 is
// NO_DATA, as for AMR-WB. AMR has no SPEECH_LOST.
const (
	FTSIDNB   = 8
	ModeNB122 = 7 // 12.2 kbit/s, the highest mode
)

// amrnbBits is the number of speech bits per frame type (RFC 4867 Table 1,
// from TS 26.101): 95, 103, 118, 134, 148, 159, 204, 244, SID 39.
var amrnbBits = [16]int{95, 103, 118, 134, 148, 159, 204, 244, 39}

var amrnb = amrFormat{name: "AMR", bits: amrnbBits, sid: FTSIDNB}

// FrameBytesNB is the byte length of an AMR frame's speech bits.
func FrameBytesNB(ft uint8) int { return amrnb.frameBytes(ft) }

// PackAMRNB builds an RFC 4867 single-channel AMR payload, bandwidth-efficient
// or octet-aligned, carrying frames in order, with codec mode request cmr.
func PackAMRNB(frames []Frame, cmr uint8, octetAligned bool) ([]byte, error) {
	return amrnb.pack(frames, cmr, octetAligned)
}

// UnpackAMRNB parses an RFC 4867 single-channel AMR payload.
func UnpackAMRNB(p []byte, octetAligned bool) (frames []Frame, cmr uint8, err error) {
	return amrnb.unpack(p, octetAligned)
}

// StorageFrameNB renders an AMR frame in the RFC 4867 §5.3 storage format
// (header byte + speech bytes), the format of opencore-amrnb's
// Encoder_Interface_Encode and Decoder_Interface_Decode.
func (f Frame) StorageFrameNB() []byte { return amrnb.storage(f) }

// FrameFromStorageNB parses one AMR storage-format frame.
func FrameFromStorageNB(b []byte) (Frame, error) { return amrnb.fromStorage(b) }
