package project

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	fontMu    sync.Mutex
	fontCache = map[string]map[string]bool{} // FONTCONFIG_FILE -> lower-case family names
)

// fontFamilyInstalled reports whether fontconfig (which FFmpeg's drawtext
// uses for font=) knows the family, case-insensitively. Without this check
// drawtext would silently fall back to another font. It runs fc-list once per
// fontconfig configuration.
func fontFamilyInstalled(family string) (bool, error) {
	fontMu.Lock()
	defer fontMu.Unlock()
	key := os.Getenv("FONTCONFIG_FILE")
	families, ok := fontCache[key]
	if !ok {
		out, err := exec.Command("fc-list", "--format", "%{family}\n").Output()
		if err != nil {
			if errors.Is(err, exec.ErrNotFound) {
				return false, errors.New("fc-list not found (install fontconfig)")
			}
			return false, err
		}
		families = map[string]bool{}
		for _, line := range strings.Split(string(out), "\n") {
			for _, name := range strings.Split(line, ",") { // a font can list several names
				if name = strings.TrimSpace(name); name != "" {
					families[strings.ToLower(name)] = true
				}
			}
		}
		fontCache[key] = families
	}
	return families[strings.ToLower(strings.TrimSpace(family))], nil
}
