package config

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

// readPropertiesFile reads a Java-style .properties file from path and returns
// a map of key→value pairs.
//
// Format rules:
//   - Lines starting with '#' or '!' (after trimming leading whitespace) are
//     comments and are ignored.
//   - Empty lines are ignored.
//   - Each non-comment line must be of the form "key=value".  Both the key and
//     the value are trimmed of surrounding whitespace.
//   - Keys do NOT include any prefix; the file contains bare keys.
//
// If path does not exist the function returns an empty map and a nil error.
// If path exists but cannot be opened (e.g. permission denied) an error is
// returned.
func readPropertiesFile(path string) (map[string]string, error) {
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	result := map[string]string{}
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines and comments.
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}

		idx := strings.IndexByte(line, '=')
		if idx < 0 {
			// No '=' found: intentionally skip these lines. This mirrors
			// carbonio-quarkus-extensions semantics — such a line is never stored,
			// so any key it might represent resolves as blank → absent in the chain.
			continue
		}

		key := strings.TrimSpace(line[:idx])
		value := strings.TrimSpace(line[idx+1:])
		if key != "" {
			result[key] = value
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return result, nil
}
