package runlog

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// maxFile is the size at which the log rotates.
const maxFile = 5 << 20

// appendLine adds line to the log at path with one write on an O_APPEND file,
// so the lines of concurrent runs do not mix.
func appendLine(path string, line []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	if full(path) {
		rotate(path, dir)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = f.Write(line)
	return errors.Join(err, f.Close())
}

func full(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.Size() >= maxFile
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
	if full(path) {
		_ = os.Rename(path, path+".1")
	}
}
