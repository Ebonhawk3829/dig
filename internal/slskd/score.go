package slskd

import (
	"fmt"
	"sort"
	"strings"
)

// ScoreWeights holds the weighted-scoring configuration.
type ScoreWeights struct {
	Completeness float64
	Dirname      float64
	Format       float64
	Source       float64
}

// Scored is a Candidate with its computed score and band.
type Scored struct {
	Candidate
	Score          float64
	Band           string // "accept", "review", "reject"
	MatchedTracks  int
	ExpectedTracks int
	FormatLabel    string // "FLAC", "MP3 320", "Mixed", ...
	RejectReason   string // non-empty when hard-rejected
}

// ScoreCandidates applies hard rejects and gates, then weighted scoring, and
// returns candidates sorted by score descending.
//
// Hard rejects (from DroppedNeedle's Soulseek path):
//   - wrong-edition markers in the folder name when the request is a studio
//     album ("remaster"/"deluxe" are NOT markers)
//   - artist-in-path gate for auto-accept: a green candidate must have the
//     artist name in the remote folder path
func ScoreCandidates(cands []Candidate, artist, album string, expectedTracks int, w ScoreWeights, acceptAt, rejectAt float64) []Scored {
	artistFold := strings.ToLower(SanitizeQuery(artist))
	albumFold := strings.ToLower(SanitizeQuery(album))
	target := artistFold + " " + albumFold

	out := make([]Scored, 0, len(cands))
	for _, c := range cands {
		s := Scored{Candidate: c, ExpectedTracks: expectedTracks}

		dirFold := strings.ToLower(c.ParentDir)

		// Hard reject: wrong-edition markers (gated on studio request — the
		// caller only passes markers when the request is a studio album).
		if reason := wrongEditionMarker(dirFold); reason != "" {
			s.Band = "reject"
			s.RejectReason = reason
			out = append(out, s)
			continue
		}

		// Per-candidate scoring
		matched := len(c.Files)
		s.MatchedTracks = matched

		completeness := 0.0
		if expectedTracks > 0 {
			completeness = float64(matched) / float64(expectedTracks)
			if completeness > 1.0 {
				completeness = 1.0
			}
		}

		dirnameMatch := similarity(dirFold, target)

		format, formatBonus := formatScore(c.Files)
		s.FormatLabel = format

		sourceBonus := 0.0
		if c.HasFreeSlot {
			sourceBonus += 0.5
		}
		speed := float64(c.UploadSpeed) / 1_000_000.0
		if speed > 1.0 {
			speed = 1.0
		}
		sourceBonus += 0.5 * speed

		s.Score = w.Completeness*completeness +
			w.Dirname*dirnameMatch +
			w.Format*formatBonus +
			w.Source*sourceBonus

		// Artist-in-path gate for auto-accept: green requires the artist name
		// in the remote folder path (title tokens alone have matched
		// wrong-artist releases on broad rungs before).
		if s.Score >= acceptAt && !strings.Contains(dirFold, artistFold) {
			s.Band = "review"
		} else if s.Score >= acceptAt {
			s.Band = "accept"
		} else if s.Score >= rejectAt {
			s.Band = "review"
		} else {
			s.Band = "reject"
		}

		out = append(out, s)
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

// wrongEditionMarker returns a reject reason if the folder name carries a
// different-product marker, else "".
func wrongEditionMarker(dirLower string) string {
	markers := []string{
		"live", "bootleg", "box set", "boxset", "box-set", "discography",
		"compilation", "greatest hits", "greatesthits", "karaoke", "tribute",
		"instrumental",
	}
	// match on word boundaries to avoid false hits inside titles
	for _, m := range markers {
		if containsWord(dirLower, m) {
			return "wrong-edition marker: " + m
		}
	}
	return ""
}

func containsWord(s, word string) bool {
	idx := 0
	for {
		i := strings.Index(s[idx:], word)
		if i < 0 {
			return false
		}
		i += idx
		before := byte(' ')
		after := byte(' ')
		if i > 0 {
			before = s[i-1]
		}
		end := i + len(word)
		if end < len(s) {
			after = s[end]
		}
		if !isWordByte(before) && !isWordByte(after) {
			return true
		}
		idx = i + 1
		if idx >= len(s) {
			return false
		}
	}
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' || b == '.' || b == '(' || b == ')' ||
		(b >= 'a' && b <= 'z') || (b >= '0' && b <= '9')
}

// formatScore returns a human label and the format bonus.
func formatScore(files []File) (string, float64) {
	if len(files) == 0 {
		return "Empty", 0.0
	}
	exts := make(map[string]int, len(files))
	for _, f := range files {
		exts[ExtensionFromFilename(f.Filename)]++
	}
	if len(exts) == 1 {
		for ext := range exts {
			if LosslessExts[ext] {
				return strings.ToUpper(ext), 1.0
			}
			// single lossy codec
			return strings.ToUpper(ext), 0.5
		}
	}
	allLossless := true
	for ext := range exts {
		if !LosslessExts[ext] {
			allLossless = false
			break
		}
	}
	if allLossless {
		return "Lossless (mixed)", 1.0
	}
	return "Mixed", 0.0
}

// similarity is 1 - levenshtein(a,b)/max(len) — a cheap dirname match.
func similarity(a, b string) float64 {
	if a == b {
		return 1.0
	}
	d := levenshtein(a, b)
	m := len(a)
	if len(b) > m {
		m = len(b)
	}
	if m == 0 {
		return 1.0
	}
	return 1.0 - float64(d)/float64(m)
}

func levenshtein(a, b string) int {
	ar := []rune(a)
	br := []rune(b)
	prev := make([]int, len(br)+1)
	cur := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(ar); i++ {
		cur[0] = i
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			cur[j] = min3(cur[j-1]+1, prev[j]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(br)]
}

func min3(a, b, c int) int {
	m := a
	if b < m {
		m = b
	}
	if c < m {
		m = c
	}
	return m
}

// FormatSpeed renders an upload speed in human units.
func FormatSpeed(bps int64) string {
	switch {
	case bps >= 1_000_000:
		return fmt.Sprintf("%.1f MB/s", float64(bps)/1_000_000)
	case bps >= 1_000:
		return fmt.Sprintf("%.0f KB/s", float64(bps)/1_000)
	default:
		return fmt.Sprintf("%d B/s", bps)
	}
}
