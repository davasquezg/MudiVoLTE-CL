package media

import (
	"bytes"
	"errors"
	"math/rand/v2"
	"testing"
)

func randomFrameNB(ft uint8) Frame {
	b := make([]byte, FrameBytesNB(ft))
	for i := range b {
		b[i] = byte(rand.IntN(256))
	}
	if bits := amrnbBits[ft] % 8; bits != 0 && len(b) > 0 {
		b[len(b)-1] &= byte(0xff << (8 - bits))
	}
	return Frame{FT: ft, Good: true, Bits: b}
}

// Every AMR mode and SID survives packing and unpacking, in both payload
// formats, next to another frame and a NO_DATA frame.
func TestAMRNBPayloadRoundTrip(t *testing.T) {
	for _, oa := range []bool{false, true} {
		for ft := uint8(0); ft <= FTSIDNB; ft++ {
			frames := []Frame{randomFrameNB(ft), randomFrameNB(ModeNB122), {FT: FTNoData, Good: true}}
			p, err := PackAMRNB(frames, 5, oa)
			if err != nil {
				t.Fatal(err)
			}
			got, cmr, err := UnpackAMRNB(p, oa)
			if err != nil || cmr != 5 || len(got) != 3 {
				t.Fatalf("oa=%v ft=%d: %v cmr=%d n=%d", oa, ft, err, cmr, len(got))
			}
			for i := range frames {
				if got[i].FT != frames[i].FT || !got[i].Good || !bytes.Equal(got[i].Bits, frames[i].Bits) {
					t.Fatalf("oa=%v ft=%d frame %d differs", oa, ft, i)
				}
			}
			// Storage format: header byte + ceil(bits/8), and back.
			st := frames[0].StorageFrameNB()
			if len(st) != 1+(amrnbBits[ft]+7)/8 || st[0] != ft<<3|0x04 {
				t.Fatalf("ft=%d storage frame %d bytes, header %08b", ft, len(st), st[0])
			}
			back, err := FrameFromStorageNB(st)
			if err != nil || back.FT != ft || !bytes.Equal(back.Bits, frames[0].Bits) {
				t.Fatalf("ft=%d storage round trip: %v", ft, err)
			}
		}
	}
}

// RFC 4867 §4.3 / §4.4 sizes of one frame of each mode: bandwidth-efficient
// is 4 (CMR) + 6 (ToC) + speech bits, rounded up to bytes; octet-aligned is
// 1 + 1 + ceil(bits/8) bytes.
func TestAMRNBPayloadSizes(t *testing.T) {
	want := map[uint8][2]int{ // mode: BE, OA bytes
		0: {14, 14}, 1: {15, 15}, 2: {16, 17}, 3: {18, 19}, 4: {20, 21}, 5: {22, 22}, 6: {27, 28}, 7: {32, 33}, 8: {7, 7},
	}
	for ft, w := range want {
		be, err := PackAMRNB([]Frame{randomFrameNB(ft)}, CMRNoPref, false)
		if err != nil {
			t.Fatal(err)
		}
		oa, err := PackAMRNB([]Frame{randomFrameNB(ft)}, CMRNoPref, true)
		if err != nil {
			t.Fatal(err)
		}
		if len(be) != w[0] || len(oa) != w[1] {
			t.Errorf("ft %d: BE %d bytes, OA %d bytes, want %v", ft, len(be), len(oa), w)
		}
		if be[0]>>4 != CMRNoPref || oa[0] != CMRNoPref<<4 || oa[1] != ft<<3|0x04 {
			t.Errorf("ft %d: headers %08b %08b %08b", ft, be[0], oa[0], oa[1])
		}
	}
	be, _ := PackAMRNB([]Frame{randomFrameNB(ModeNB122)}, CMRNoPref, false)
	if _, _, err := UnpackAMRNB(be[:20], false); err == nil {
		t.Fatal("truncated payload accepted")
	}
}

// RFC 4867 §4.3.2: frame types 9–14 do not exist for AMR (14 SPEECH_LOST is
// AMR-WB only); such packets are discarded, and are never built.
func TestAMRNBInvalidFrameTypes(t *testing.T) {
	for ft := uint8(9); ft <= 14; ft++ {
		for _, oa := range []bool{false, true} {
			// A ToC with F=0 and this FT, followed by enough bytes for any frame.
			p := make([]byte, 40)
			if oa {
				p[0], p[1] = CMRNoPref<<4, ft<<3|0x04
			} else {
				p[0], p[1] = CMRNoPref<<4|ft>>1, (ft&1)<<7|0x40 // F=0 FT Q=1
			}
			if _, _, err := UnpackAMRNB(p, oa); err == nil {
				t.Errorf("oa=%v: FT %d accepted", oa, ft)
			}
		}
		if _, err := PackAMRNB([]Frame{{FT: ft}}, CMRNoPref, false); err == nil {
			t.Errorf("FT %d packed", ft)
		}
	}
	// The same ToC with FT 14 is a valid AMR-WB SPEECH_LOST frame.
	if _, _, err := UnpackAMRWB([]byte{CMRNoPref<<4 | 14>>1, 0x40}, false); err != nil {
		t.Errorf("AMR-WB SPEECH_LOST: %v", err)
	}
}

// fakeNB stands in for opencore-amrnb: a storage frame of the requested
// mode whose first speech byte is a counter, decoded to 160 samples that
// carry the frame type and that byte.
type fakeNB struct {
	modes  []int
	n      byte
	closed bool
}

func (f *fakeNB) EncodeFrame(pcm []int16, mode int) ([]byte, error) {
	if len(pcm) != 160 {
		return nil, errors.New("not 160 samples")
	}
	f.modes = append(f.modes, mode)
	f.n++
	fr := randomFrameNB(uint8(mode))
	fr.Bits[0] = f.n
	return fr.StorageFrameNB(), nil
}

func (f *fakeNB) DecodeFrame(st []byte) ([]int16, error) {
	pcm := make([]int16, 160)
	pcm[0] = int16(st[0] >> 3)
	if len(st) > 1 {
		pcm[1] = int16(st[1])
	}
	return pcm, nil
}

func (f *fakeNB) Close() { f.closed = true }

func TestAMRNBCodec(t *testing.T) {
	old := NewAMRNBCoder
	defer func() { NewAMRNBCoder = old }()
	NewAMRNBCoder = nil
	if _, err := NewCodec(Params{Name: "AMR"}); err == nil {
		t.Fatal("AMR without a linked codec")
	}
	var fakes []*fakeNB
	NewAMRNBCoder = func() (FrameCoder, error) { f := &fakeNB{}; fakes = append(fakes, f); return f, nil }
	for _, tc := range []struct {
		fmtp  string
		ptime int
		mode  int
		oa    bool
	}{
		{"", 0, 7, false},
		{"mode-set=7", 20, 7, false},
		{"mode-set=0,2,4;octet-align=1", 40, 4, true},
		{"mode-set=8,9;octet-align=0", 20, 7, false}, // no AMR mode in the set: the default
		{"octet-align=1;mode-set=2", 60, 2, true},
	} {
		fakes = nil
		c, err := NewCodec(Params{Name: "amr", Fmtp: tc.fmtp, Ptime: tc.ptime})
		if err != nil {
			t.Fatal(err)
		}
		if c.Name() != "AMR" || c.SampleRate() != 8000 {
			t.Fatalf("%s at %d Hz", c.Name(), c.SampleRate())
		}
		frames := max(tc.ptime, 20) / 20
		if _, err := c.Encode(make([]int16, 320*frames)); err == nil {
			t.Fatalf("fmtp %q: 16 kHz frame accepted", tc.fmtp)
		}
		p, err := c.Encode(make([]int16, 160*frames))
		if err != nil {
			t.Fatal(err)
		}
		got, _, err := UnpackAMRNB(p, tc.oa)
		if err != nil || len(got) != frames {
			t.Fatalf("fmtp %q: %d frames, %v", tc.fmtp, len(got), err)
		}
		for i, f := range got {
			if int(f.FT) != tc.mode || f.Bits[0] != byte(i+1) || fakes[0].modes[i] != tc.mode {
				t.Fatalf("fmtp %q frame %d: FT %d, counter %d", tc.fmtp, i, f.FT, f.Bits[0])
			}
		}
		pcm, err := c.Decode(p)
		if err != nil || len(pcm) != 160*frames || pcm[0] != int16(tc.mode) || pcm[1] != 1 {
			t.Fatalf("fmtp %q: decode %d samples %v", tc.fmtp, len(pcm), err)
		}
		Close(c)
		if !fakes[0].closed {
			t.Fatal("codec state not freed")
		}
	}
}
