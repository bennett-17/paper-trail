// Package envfile provides minimal ".env" file loading with no
// third-party dependencies, so paper-trail can stay on the standard
// library only. It is intentionally simple: KEY=VALUE lines, optional
// quotes, "#" comments, blank lines ignored.
package envfile

import (
	"bufio"
	"os"
	"strings"
)

// Load reads KEY=VALUE pairs from the file at path and applies them via
// os.Setenv. Variables already present in the environment are left
// untouched, so a real `export FOO=bar` always takes precedence over
// whatever is in the file. A missing file is not an error — .env is
// optional local configuration, not a requirement.
func Load(path string) error {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		value = strings.TrimSpace(value)
		value = strings.Trim(value, `"'`)

		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		os.Setenv(key, value)
	}
	return scanner.Err()
}
