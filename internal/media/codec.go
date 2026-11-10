package media

import "fmt"

// Codec configuration records shared by MP4 and MKV: profile, bit depth,
// chroma (→ ffmpeg-style pix_fmt), and audio channel layouts.

// videoConfig holds video codec details from a configuration record.
type videoConfig struct {
	profile  string // "" = unknown
	bitDepth uint8
	// chroma: 0 = mono, 1 = 4:2:0, 2 = 4:2:2, 3 = 4:4:4; -1 = unknown
	chroma int
}

func noConfig() videoConfig { return videoConfig{chroma: -1} }

func (c videoConfig) apply(t *Track) {
	if c.profile != "" {
		t.Profile = c.profile
	}
	if c.bitDepth > 0 {
		t.BitDepth = c.bitDepth
	}
	if c.chroma >= 0 {
		t.PixFmt = pixFmt(uint8(c.chroma), max(t.BitDepth, 8))
	}
}

func pixFmt(chroma, depth uint8) string {
	base := "yuv420p"
	switch chroma {
	case 0:
		base = "gray"
	case 2:
		base = "yuv422p"
	case 3:
		base = "yuv444p"
	}
	if depth > 8 {
		return fmt.Sprintf("%s%dle", base, depth)
	}
	return base
}

// avcc parses an avcC record (ISO/IEC 14496-15).
func avcc(b []byte) videoConfig {
	profileIDC, ok := u8At(b, 1)
	if !ok {
		return noConfig()
	}
	constrained := u8Or(b, 2, 0)&0x40 != 0
	profile := ""
	switch profileIDC {
	case 66:
		profile = "baseline"
		if constrained {
			profile = "constrained baseline"
		}
	case 77:
		profile = "main"
	case 88:
		profile = "extended"
	case 100:
		profile = "high"
	case 110:
		profile = "high 10"
	case 122:
		profile = "high 4:2:2"
	case 244:
		profile = "high 4:4:4 predictive"
	case 44:
		profile = "cavlc 4:4:4"
	}
	cfg := videoConfig{profile: profile, bitDepth: 8, chroma: 1}
	// High profiles append chroma format and bit depth after the SPS/PPS lists.
	switch profileIDC {
	case 100, 110, 122, 144, 244, 44:
		i := 5
		nSPS := int(u8Or(b, i, 0) & 0x1f)
		i++
		for range nSPS {
			l, ok := u16At(b, i)
			if !ok {
				return cfg
			}
			i += 2 + int(l)
		}
		nPPS := int(u8Or(b, i, 0))
		i++
		for range nPPS {
			l, ok := u16At(b, i)
			if !ok {
				return cfg
			}
			i += 2 + int(l)
		}
		c, ok1 := u8At(b, i)
		d, ok2 := u8At(b, i+1)
		if ok1 && ok2 {
			cfg.chroma = int(c & 3)
			cfg.bitDepth = d&7 + 8
		}
	}
	return cfg
}

// hvcc parses an hvcC record (ISO/IEC 14496-15).
func hvcc(b []byte) videoConfig {
	cfg := noConfig()
	if p, ok := u8At(b, 1); ok {
		switch p & 0x1f {
		case 1:
			cfg.profile = "main"
		case 2:
			cfg.profile = "main 10"
		case 3:
			cfg.profile = "main still picture"
		case 4:
			cfg.profile = "rext"
		default:
			cfg.profile = fmt.Sprintf("profile %d", p&0x1f)
		}
	}
	if d, ok := u8At(b, 17); ok {
		cfg.bitDepth = d&7 + 8
	}
	if c, ok := u8At(b, 16); ok {
		cfg.chroma = int(c & 3)
	}
	return cfg
}

// av1c parses an av1C record (AV1 codec ISOBMFF binding).
func av1c(b []byte) videoConfig {
	b1, ok1 := u8At(b, 1)
	b2, ok2 := u8At(b, 2)
	if !ok1 || !ok2 {
		return noConfig()
	}
	profile := "unknown"
	switch b1 >> 5 {
	case 0:
		profile = "main"
	case 1:
		profile = "high"
	case 2:
		profile = "professional"
	}
	depth := uint8(8)
	if b2&0x40 != 0 {
		depth = 10
		if b2&0x20 != 0 {
			depth = 12
		}
	}
	chroma := 3
	switch {
	case b2&0x10 != 0:
		chroma = 0
	case b2&0x08 != 0 && b2&0x04 != 0:
		chroma = 1
	case b2&0x08 != 0:
		chroma = 2
	}
	return videoConfig{profile: profile, bitDepth: depth, chroma: chroma}
}

// vpcc parses a vpcC record (VP codec ISOBMFF binding, a FullBox).
func vpcc(b []byte) videoConfig {
	p, ok1 := u8At(b, 4)
	d, ok2 := u8At(b, 6)
	if !ok1 || !ok2 {
		return noConfig()
	}
	chroma := 1
	switch (d >> 1) & 7 {
	case 2:
		chroma = 2
	case 3:
		chroma = 3
	}
	return videoConfig{profile: fmt.Sprintf("profile %d", p), bitDepth: d >> 4, chroma: chroma}
}

// doviProfile is the Dolby Vision profile of a dvcC/dvvC/dvwC payload.
func doviProfile(b []byte) uint8 {
	return u8Or(b, 2, 0) >> 1
}

// acmodChannels: channels for an AC-3/E-AC-3 acmod plus LFE.
func acmodChannels(acmod uint8, lfe bool) uint8 {
	n := [8]uint8{2, 1, 2, 3, 3, 4, 4, 5}[acmod&7]
	if lfe {
		n++
	}
	return n
}

// dac3 is the AC-3 channel count of a dac3 box.
func dac3(b []byte) (uint8, bool) {
	b1, ok := u8At(b, 1)
	if !ok {
		return 0, false
	}
	return acmodChannels((b1>>3)&7, (b1>>2)&1 == 1), true
}

type bitReader struct {
	b   []byte
	pos int
	err bool
}

func (r *bitReader) read(n int) uint32 {
	var v uint32
	for range n {
		if r.pos/8 >= len(r.b) {
			r.err = true
			return 0
		}
		v = v<<1 | uint32(r.b[r.pos/8]>>(7-r.pos%8)&1)
		r.pos++
	}
	return v
}

// dec3 is the E-AC-3 channel count and Atmos (JOC extension flag, ETSI TS
// 103 420) of a dec3 box.
func dec3(b []byte) (channels uint8, atmos bool, ok bool) {
	r := &bitReader{b: b}
	r.read(13) // data_rate
	numIndSub := r.read(3) + 1
	if r.err {
		return 0, false, false
	}
	for i := uint32(0); i < numIndSub; i++ {
		r.read(2) // fscod
		r.read(5) // bsid
		r.read(1) // reserved
		r.read(1) // asvc
		r.read(3) // bsmod
		acmod := uint8(r.read(3))
		lfe := r.read(1) == 1
		r.read(3) // reserved
		numDepSub := r.read(4)
		var extra uint8
		if numDepSub > 0 {
			loc := r.read(9)
			// Lc/Rc, Lrs/Rrs, Cs, Ts, Lsd/Rsd, Lw/Rw, Lvh/Rvh, Cvh, LFE2
			for _, bn := range [][2]uint8{{8, 2}, {7, 2}, {6, 1}, {5, 1}, {4, 2}, {3, 2}, {2, 2}, {1, 1}, {0, 1}} {
				if loc&(1<<bn[0]) != 0 {
					extra += bn[1]
				}
			}
		} else {
			r.read(1)
		}
		if r.err {
			return 0, false, false
		}
		if i == 0 {
			channels = acmodChannels(acmod, lfe) + extra
		}
	}
	r.read(7)
	if !r.err {
		atmos = r.read(1) == 1 && !r.err
	}
	return channels, atmos, true
}
