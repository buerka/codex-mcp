package output

import (
	"fmt"
	"io"
	"os"
)

// Size returns the file size, or 0 when the file does not exist yet.
func Size(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// ReadPage returns up to max bytes at offset and reports the total size and
// whether further bytes remain. Offsets always refer to the raw file.
func ReadPage(path string, offset, max int) (text string, size int64, more bool, err error) {
	if offset < 0 {
		return "", 0, false, fmt.Errorf("offset must not be negative")
	}
	if max <= 0 {
		max = 32 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return "", 0, false, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", 0, false, err
	}
	size = info.Size()
	if int64(offset) >= size {
		return "", size, false, nil
	}
	n := int64(max)
	if int64(offset)+n > size {
		n = size - int64(offset)
	}
	buf, err := io.ReadAll(io.NewSectionReader(f, int64(offset), n))
	if err != nil {
		return "", size, false, err
	}
	return string(buf), size, int64(offset)+int64(len(buf)) < size, nil
}

// HeadTail returns the first two thirds and last third of a file, with a note
// naming the byte offset of the omitted middle.
func HeadTail(path string, max int, id string) (string, error) {
	if max <= 0 {
		max = 8 * 1024
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	size := info.Size()
	if size <= int64(max) {
		b, err := io.ReadAll(f)
		return string(b), err
	}
	headN := max * 2 / 3
	tailN := max - headN
	head := make([]byte, headN)
	if _, err := io.ReadFull(io.NewSectionReader(f, 0, int64(headN)), head); err != nil {
		return "", err
	}
	tail := make([]byte, tailN)
	if _, err := io.ReadFull(io.NewSectionReader(f, size-int64(tailN), int64(tailN)), tail); err != nil {
		return "", err
	}
	omitted := size - int64(headN+tailN)
	return fmt.Sprintf("%s\n...[%d bytes omitted; fetch with codex_output id=%s offset=%d]...\n%s",
		head, omitted, id, headN, tail), nil
}

// Tail returns the last max bytes of a file.
func Tail(path string, max int) (string, error) {
	if max <= 0 {
		return "", nil
	}
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := info.Size() - int64(max)
	if start < 0 {
		start = 0
	}
	b, err := io.ReadAll(io.NewSectionReader(f, start, info.Size()-start))
	return string(b), err
}
