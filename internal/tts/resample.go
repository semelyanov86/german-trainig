package tts

import (
	"fmt"
	"math"
)

// phoneRate is the sample rate of an Asterisk channel and of the .sln files
// played to it.
const phoneRate = 8000

// decimator brings streamed PCM down to the 8 kHz of a phone channel. Dropping
// samples alone would fold everything above 4 kHz back into the speech band as
// hiss, so a windowed-sinc low-pass filter runs first. The filter keeps its
// history between calls because the audio arrives in pieces of up to half a
// second, and a filter restarted on every piece would click at each seam.
type decimator struct {
	factor int
	taps   []float64
	buf    []float64 // input not consumed yet, led by the filter history
}

func newDecimator(inRate int) (*decimator, error) {
	if inRate <= 0 || inRate%phoneRate != 0 {
		return nil, fmt.Errorf("unsupported sample rate %d", inRate)
	}
	d := &decimator{factor: inRate / phoneRate}
	if d.factor == 1 {
		return d, nil
	}
	// 95 Hamming-windowed taps put the transition between ~3.0 and ~3.8 kHz at
	// 24 kHz input: the 300-3400 Hz telephone band passes, and what would alias
	// above 4 kHz is ~50 dB down.
	const n = 95
	cutoff := 3400.0 / float64(inRate)
	d.taps = make([]float64, n)
	sum := 0.0
	for i := range d.taps {
		m := float64(i - (n-1)/2)
		h := 2 * cutoff
		if m != 0 {
			h = math.Sin(2*math.Pi*cutoff*m) / (math.Pi * m)
		}
		d.taps[i] = h * (0.54 - 0.46*math.Cos(2*math.Pi*float64(i)/float64(n-1)))
		sum += d.taps[i]
	}
	for i := range d.taps {
		d.taps[i] /= sum // unity gain at DC
	}
	// Half a filter of leading zeros centres the first output on the first
	// input sample, so the stream is neither delayed nor clipped at its start.
	d.buf = make([]float64, (n-1)/2)
	return d, nil
}

// push filters a piece of input and returns the 8 kHz samples it completes.
func (d *decimator) push(in []int16) []int16 {
	if d.factor == 1 {
		return append([]int16(nil), in...)
	}
	for _, s := range in {
		d.buf = append(d.buf, float64(s))
	}
	n := len(d.taps)
	out := make([]int16, 0, len(d.buf)/d.factor)
	i := 0
	for ; i+n <= len(d.buf); i += d.factor {
		acc := 0.0
		for k, t := range d.taps {
			acc += t * d.buf[i+k]
		}
		out = append(out, clamp16(acc))
	}
	d.buf = append(d.buf[:0], d.buf[i:]...)
	return out
}

// flush returns the samples still held back by the filter at the end of a
// stream.
func (d *decimator) flush() []int16 {
	if d.factor == 1 {
		return nil
	}
	return d.push(make([]int16, (len(d.taps)-1)/2))
}

func clamp16(v float64) int16 {
	switch {
	case v > math.MaxInt16:
		return math.MaxInt16
	case v < math.MinInt16:
		return math.MinInt16
	}
	return int16(math.Round(v))
}
