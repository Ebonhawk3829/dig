package slskd

import (
	"path"
	"sort"
	"strings"
)

// Candidate is one grouped folder of files from a single peer — a potential
// download of the album.
type Candidate struct {
	Username     string
	ParentDir    string // remote album directory (disc-level dirs collapsed)
	Files        []File
	HasFreeSlot  bool
	UploadSpeed  int64
	QueueLength  int
}

// AudioExts are extensions dig treats as audio files.
var AudioExts = map[string]bool{
	"flac": true, "mp3": true, "ogg": true, "opus": true, "m4a": true,
	"aac": true, "wav": true, "ape": true, "wv": true, "alac": true,
	"aiff": true, "aif": true, "wma": true,
}

// LosslessExts are extensions dig treats as lossless.
var LosslessExts = map[string]bool{
	"flac": true, "wav": true, "ape": true, "wv": true, "alac": true,
	"aiff": true, "aif": true,
}

// GroupResponses flattens peer responses into candidates keyed by
// (username, parent_directory), walking up past Disc N / CD N directories so
// multi-disc albums group at the album level.
func GroupResponses(responses []UserResponse) []Candidate {
	type key struct{ user, dir string }
	groups := make(map[key]*Candidate)

	for _, r := range responses {
		for _, f := range r.Files {
			ext := ExtensionFromFilename(f.Filename)
			if !AudioExts[ext] {
				continue
			}
			dir := AlbumDir(f.Filename)
			k := key{r.Username, dir}
			c, ok := groups[k]
			if !ok {
				c = &Candidate{
					Username:    r.Username,
					ParentDir:   dir,
					HasFreeSlot: r.HasFreeUploadSlot,
					UploadSpeed: r.UploadSpeed,
					QueueLength: r.QueueLength,
				}
				groups[k] = c
			}
			c.Files = append(c.Files, f)
		}
	}

	out := make([]Candidate, 0, len(groups))
	for _, c := range groups {
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Username != out[j].Username {
			return out[i].Username < out[j].Username
		}
		return out[i].ParentDir < out[j].ParentDir
	})
	return out
}

// ExtensionFromFilename returns the lowercase extension, falling back to
// parsing the filename because slskd's extension field is unreliable.
func ExtensionFromFilename(filename string) string {
	name := filename
	if i := strings.LastIndexByte(name, '\\'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '/'); i >= 0 {
		name = name[i+1:]
	}
	if i := strings.LastIndexByte(name, '.'); i < 0 {
		return ""
	}
	return strings.ToLower(name[strings.LastIndexByte(name, '.')+1:])
}

// AlbumDir extracts the album-level directory from a Soulseek path, walking
// up past "Disc N" / "CD N" components. Soulseek paths use backslashes.
func AlbumDir(filename string) string {
	// normalise separators
	p := strings.ReplaceAll(filename, "\\", "/")
	dir := path.Dir(p)
	parts := strings.Split(dir, "/")

	for len(parts) > 1 {
		last := strings.ToLower(parts[len(parts)-1])
		if isDiscDir(last) {
			parts = parts[:len(parts)-1]
			continue
		}
		break
	}
	return strings.Join(parts, "\\")
}

func isDiscDir(name string) bool {
	name = strings.TrimSpace(name)
	for _, prefix := range []string{"disc", "cd", "disk"} {
		if strings.HasPrefix(name, prefix) {
			rest := strings.TrimPrefix(name, prefix)
			rest = strings.TrimSpace(rest)
			if rest == "" {
				return true
			}
			// "disc 1", "cd1", "disc one" handled loosely: digit or single word
			if len(rest) <= 4 {
				return true
			}
		}
	}
	return false
}
