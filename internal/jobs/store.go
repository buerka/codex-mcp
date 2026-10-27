package jobs

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func fileSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// scanNext finds the highest existing cx-N directory so a restart never
// overwrites a previous job.
func scanNext(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, err
	}
	max := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		if n, ok := jobNumber(e.Name()); ok && n > max {
			max = n
		}
	}
	return max + 1, nil
}

func jobNumber(name string) (int, bool) {
	if !strings.HasPrefix(name, "cx-") {
		return 0, false
	}
	n, err := strconv.Atoi(name[3:])
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

func jobPaths(dir, id string) Paths {
	d := filepath.Join(dir, id)
	return Paths{
		Dir:    d,
		Final:  filepath.Join(d, "final.txt"),
		Events: filepath.Join(d, "events.jsonl"),
		Stderr: filepath.Join(d, "stderr.log"),
		Schema: filepath.Join(d, "schema.json"),
	}
}

const defaultGrace = 5 * time.Second
