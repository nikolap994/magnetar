package storage

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Magnetar patch test: writes and reads through cached handles must never see
// a handle closed by closeWriters or rename running at the same time.
func TestClassicFileIoNoCloseUnderUse(t *testing.T) {
	dir := t.TempDir()
	fio := newClassicFileIo()
	defer fio.Close()
	part := filepath.Join(dir, "a.bin.part")
	block := make([]byte, 16<<10)

	var wg sync.WaitGroup
	errs := make(chan error, 64)
	stop := make(chan struct{})
	worker := func(read bool) {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			off := int64(i%64) * int64(len(block))
			if read {
				r, err := fio.openForRead(part)
				if errors.Is(err, os.ErrNotExist) {
					continue
				}
				if err != nil {
					errs <- err
					return
				}
				_, err = r.writeToN(io.Discard, int64(len(block)))
				r.Close()
				if err != nil && !errors.Is(err, io.EOF) {
					errs <- err
					return
				}
				continue
			}
			w, err := fio.openForWrite(part, 0)
			if err != nil {
				errs <- err
				return
			}
			_, err = w.WriteAt(block, off)
			w.Close()
			if err != nil {
				errs <- err
				return
			}
		}
	}
	for i := 0; i < 4; i++ {
		wg.Add(2)
		go worker(false)
		go worker(true)
	}
	for i := 0; i < 200; i++ {
		if _, _, err := fio.closeWriters(); err != nil {
			t.Fatal(err)
		}
		if err := fio.flush(part, 0, 1); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		final := filepath.Join(dir, "a.bin")
		if err := fio.rename(part, final); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		os.Remove(final)
	}
	close(stop)
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("operation failed while handles were closed concurrently: %v", err)
	}
}
