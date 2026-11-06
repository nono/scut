package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// maxFile is the size at which the log rotates.
const maxFile = 5 << 20

// hardLimit is the size at which the log stops, when it cannot rotate.
const hardLimit = 2 * maxFile

// appendLine adds line to the log at path with one write on an O_APPEND file,
// so the lines of concurrent runs do not mix. It returns an error and writes
// nothing when the log cannot be private, is not a regular file, or is at
// hardLimit.
func appendLine(path string, line []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		return err
	}
	if logSize(path) >= maxFile {
		rotate(path, dir)
	}
	if logSize(path) >= hardLimit {
		return errors.New("the log is at its hard limit")
	}
	// O_NONBLOCK: the open of a FIFO must not wait for a reader.
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR|syscall.O_NONBLOCK, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if !fi.Mode().IsRegular() {
		return errors.New("the log is not a regular file")
	}
	if err := f.Chmod(0o600); err != nil {
		return err
	}
	if endsTorn(f, fi.Size()) {
		line = append([]byte("\n"), line...)
	}
	_, err = f.Write(line)
	return err
}

// endsTorn tells if a short write of an earlier run left a line with no
// newline. The next line must not join it.
func endsTorn(f *os.File, size int64) bool {
	if size == 0 {
		return false
	}
	var last [1]byte
	_, err := f.ReadAt(last[:], size-1)
	return err == nil && last[0] != '\n'
}

// logSize returns the size of the log, or 0 when it cannot read it.
func logSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// rotate renames a full log to path.1. Two runs can both see a full log. A
// second rename would replace the full .1 with a small file, so the rename
// needs the lock on dir, and a second look at the size. A run that does not get
// the lock does not wait: it writes to the log and leaves the rename to the
// other run. A failure here does not stop the write.
func rotate(path, dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	defer func() { _ = d.Close() }() // also releases the lock
	if syscall.Flock(int(d.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) != nil {
		return
	}
	if logSize(path) >= maxFile {
		_ = os.Rename(path, path+".1")
	}
}
