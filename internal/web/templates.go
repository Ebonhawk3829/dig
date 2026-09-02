package web

import (
	"embed"
	"fmt"
	"html/template"
	"strings"
)

//go:embed templates/*.html
var templates embed.FS

// templateFuncs are the helper functions available in templates.
var templateFuncs = template.FuncMap{
	"mul100": func(f float64) float64 {
		return f * 100
	},
	"mul100n": func(f float64) string {
		return itoa(int(f*100 + 0.5))
	},
	"speed": func(bps int64) string {
		switch {
		case bps >= 1_000_000:
			return fmt.Sprintf("%.1f MB/s", float64(bps)/1_000_000)
		case bps >= 1_000:
			return fmt.Sprintf("%.0f KB/s", float64(bps)/1_000)
		default:
			return fmt.Sprintf("%d B/s", bps)
		}
	},
	"size": func(b int64) string {
		switch {
		case b >= 1<<20:
			return fmt.Sprintf("%.1f MB", float64(b)/(1<<20))
		case b >= 1<<10:
			return fmt.Sprintf("%.0f KB", float64(b)/(1<<10))
		default:
			return fmt.Sprintf("%d B", b)
		}
	},
	"filename": func(fn string) string {
		if i := strings.LastIndexAny(fn, "\\/"); i >= 0 {
			return fn[i+1:]
		}
		return fn
	},
	"lower": strings.ToLower,
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
