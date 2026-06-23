// Package genre maps a DEFQON.1 stage to its primary musical genre, used as the
// ID3 genre tag of per-set split files. The mapping is static; the global
// ID3_GENRE value is used as a fallback for stages that are not listed here
// (e.g. an ad-hoc test channel).
package genre

import "strings"

// stageGenres maps a stage name (upper-cased) to its primary genre. These are
// the signature sounds of each DEFQON.1 stage.
var stageGenres = map[string]string{
	"RED":     "Hardstyle",
	"MAGENTA": "Hardstyle Classics",
	"PURPLE":  "Hardstyle",
	"WHITE":   "Freestyle",
	"BROWN":   "Freestyle",
	"PINK":    "Drum & Bass",
	"BLUE":    "Rawstyle",
	"INDIGO":  "Xtra Raw",
	"YELLOW":  "Uptempo Hardcore",
	"ORANGE":  "Hard Trance",
	"SILVER":  "Industrial Hardcore",
	"GREEN":   "Hard Techno",
	"GOLD":    "Millennium Hardcore",
	"BLACK":   "Hardcore",
	"UV":      "Euphoric Hardstyle",
}

// ForStage returns the genre for a stage, matched case-insensitively. Unknown
// stages fall back to the provided default (and ultimately to "Hardstyle").
func ForStage(stage, fallback string) string {
	if g, ok := stageGenres[strings.ToUpper(strings.TrimSpace(stage))]; ok {
		return g
	}
	if fallback != "" {
		return fallback
	}
	return "Hardstyle"
}
