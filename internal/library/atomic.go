package library

import (
	"fmt"
	"os"
	"path/filepath"
)

// tempName is the in-progress name for a file. The "~$" prefix and ".part"
// suffix are both on the ownCloud and Dropbox exclude lists, so a half
// written file is never synced.
func tempName(name string) string {
	return "~$" + name + ".part"
}

// writeFileAtomic replaces path with data so that a reader (or a sync
// client) sees either the old or the new content, never a partial file.
func writeFileAtomic(path string, data []byte) (err error) {
	dir, name := filepath.Split(path)
	tmp := filepath.Join(dir, tempName(name))
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return fmt.Errorf("write %s: %w", name, err)
	}
	defer func() {
		if err != nil {
			f.Close()
			os.Remove(tmp)
			err = fmt.Errorf("write %s: %w", name, err)
		}
	}()
	if _, err = f.Write(data); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	if err = os.Rename(tmp, path); err != nil {
		return err
	}
	syncDir(dir)
	return nil
}

// syncDir makes the rename durable where the OS supports syncing a
// directory; it is best effort because Windows and some network mounts
// refuse it.
func syncDir(dir string) {
	d, err := os.Open(dir)
	if err != nil {
		return
	}
	_ = d.Sync()
	d.Close()
}
