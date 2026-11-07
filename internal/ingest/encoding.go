package ingest

import (
	"bytes"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
)

// decodeText turns file bytes into a Go string.
// Order: BOM (UTF-8, UTF-16 LE/BE), a strong UTF-16 Cyrillic or ASCII pattern,
// valid UTF-8, then windows-1251 or koi8-r by Cyrillic letter count.
func decodeText(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
		return string(raw[3:])
	}
	if bytes.HasPrefix(raw, []byte{0xFF, 0xFE}) {
		return decodeUTF16(raw[2:], true)
	}
	if bytes.HasPrefix(raw, []byte{0xFE, 0xFF}) {
		return decodeUTF16(raw[2:], false)
	}
	if s, ok := detectUTF16(raw); ok {
		return s
	}
	if utf8.Valid(raw) {
		return string(raw)
	}
	cp := decodeCharmap(raw, charmap.Windows1251)
	koi := decodeCharmap(raw, charmap.KOI8R)
	// Both charmaps turn high bytes into Cyrillic, so a letter count ties.
	// Common Russian bigrams pick the readable decoding.
	if rusScore(koi) > rusScore(cp) {
		return koi
	}
	return cp
}

func decodeCharmap(raw []byte, enc encoding.Encoding) string {
	out, err := enc.NewDecoder().Bytes(raw)
	if err != nil {
		return ""
	}
	return string(out)
}

func decodeUTF16(raw []byte, little bool) string {
	if len(raw)%2 == 1 {
		raw = raw[:len(raw)-1]
	}
	units := make([]uint16, len(raw)/2)
	for i := range units {
		if little {
			units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
		} else {
			units[i] = uint16(raw[2*i+1]) | uint16(raw[2*i])<<8
		}
	}
	return string(utf16.Decode(units))
}

// detectUTF16 catches UTF-16 without a BOM.
// Cyrillic lives in U+0400, so the 0x04 byte sits on every other position,
// and that byte stream is still valid UTF-8. A plain Valid check is not enough.
func detectUTF16(raw []byte) (string, bool) {
	if len(raw) < 8 || len(raw)%2 != 0 {
		return "", false
	}
	pairs := len(raw) / 2
	leCyr, beCyr := 0, 0
	leASCII, beASCII := 0, 0
	for i := 0; i < len(raw); i += 2 {
		lo, hi := raw[i], raw[i+1]
		if hi == 0x04 {
			leCyr++
		}
		if lo == 0x04 {
			beCyr++
		}
		if hi == 0 && lo != 0 {
			leASCII++
		}
		if lo == 0 && hi != 0 {
			beASCII++
		}
	}
	switch {
	case leCyr*3 > pairs && leCyr > beCyr:
		return decodeUTF16(raw, true), true
	case beCyr*3 > pairs && beCyr > leCyr:
		return decodeUTF16(raw, false), true
	case leASCII*5 > pairs*2 && leASCII > beASCII:
		return decodeUTF16(raw, true), true
	case beASCII*5 > pairs*2 && beASCII > leASCII:
		return decodeUTF16(raw, false), true
	default:
		return "", false
	}
}

var rusBigrams = []string{
	"ст", "но", "ен", "то", "на", "ов", "ни", "ра", "ко", "ро",
	"ан", "ос", "по", "ер", "ол", "ал", "ре", "пр", "ли", "ел",
	"те", "ек", "ак", "ка", "от", "ор", "во", "го", "не",
}

func rusScore(s string) int {
	low := strings.ToLower(s)
	n := 0
	for _, pair := range rusBigrams {
		n += strings.Count(low, pair)
	}
	return n
}
