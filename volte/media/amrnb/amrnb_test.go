//go:build cgo

package amrnb

import (
	"math"
	"testing"

	"github.com/xarvel/MudiVoLTE/volte/media"
)

// goertzel returns the power of freq in pcm.
func goertzel(pcm []int16, rate int, freq float64) float64 {
	k := 2 * math.Cos(2*math.Pi*freq/float64(rate))
	var s1, s2 float64
	for _, x := range pcm {
		s := float64(x) + k*s1 - s2
		s2, s1 = s1, s
	}
	return s1*s1 + s2*s2 - k*s1*s2
}

func toneFrame(f int) []int16 {
	pcm := make([]int16, 160)
	for i := range pcm {
		pcm[i] = int16(6000 * math.Sin(2*math.Pi*1000*float64(f*160+i)/8000))
	}
	return pcm
}

// Every mode encodes to a storage frame of its own type and of the size of
// RFC 4867 Table 1 (header byte + ceil(bits/8)).
func TestStorageFrameSizes(t *testing.T) {
	fc, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer fc.Close()
	for mode := 0; mode <= media.ModeNB122; mode++ {
		st, err := fc.EncodeFrame(toneFrame(mode), mode)
		if err != nil {
			t.Fatal(err)
		}
		if ft := int(st[0] >> 3 & 15); ft != mode || st[0]&0x04 == 0 {
			t.Errorf("mode %d: header %08b", mode, st[0])
		}
		if want := 1 + media.FrameBytesNB(uint8(mode)); len(st) != want {
			t.Errorf("mode %d: %d bytes, want %d", mode, len(st), want)
		}
	}
	if _, err := fc.EncodeFrame(make([]int16, 160), 8); err == nil {
		t.Error("mode 8 accepted")
	}
}

// A 1 kHz tone survives encode → RFC 4867 packing → decode in both payload
// formats, at 12.2 kbit/s and at the low modes the network offers.
func TestCodecThroughPayload(t *testing.T) {
	for _, fmtp := range []string{"mode-set=7", "mode-set=0,2,4;octet-align=1", "octet-align=1", ""} {
		enc, err := media.NewCodec(media.Params{Name: "AMR", Fmtp: fmtp})
		if err != nil {
			t.Fatal(err)
		}
		dec, _ := media.NewCodec(media.Params{Name: "AMR", Fmtp: fmtp})
		if enc.SampleRate() != 8000 || enc.Name() != "AMR" {
			t.Fatalf("%s at %d Hz", enc.Name(), enc.SampleRate())
		}
		var out []int16
		for f := 0; f < 50; f++ {
			payload, err := enc.Encode(toneFrame(f))
			if err != nil {
				t.Fatal(err)
			}
			got, err := dec.Decode(payload)
			if err != nil {
				t.Fatal(err)
			}
			out = append(out, got...)
		}
		tail := out[len(out)/2:]
		p1k, p3k := goertzel(tail, 8000, 1000), goertzel(tail, 8000, 3000)
		if p1k < 100*p3k {
			t.Errorf("fmtp %q: 1 kHz not dominant (%.3g vs %.3g)", fmtp, p1k, p3k)
		}
		media.Close(enc)
		media.Close(dec)
	}
}
