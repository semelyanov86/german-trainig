package theme

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"time"
)

func RandomTheme(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open themes file %s: %w", path, err)
	}
	defer f.Close()

	var themes []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line != "" {
			themes = append(themes, line)
		}
	}
	if err := sc.Err(); err != nil {
		return "", fmt.Errorf("read themes file: %w", err)
	}
	if len(themes) == 0 {
		return "", fmt.Errorf("no themes found in %s", path)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	return themes[rng.Intn(len(themes))], nil
}
