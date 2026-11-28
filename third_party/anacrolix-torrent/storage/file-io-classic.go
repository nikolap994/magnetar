package storage

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"sort"
	"sync"
)

// Magnetar patch: cached write handles are reference counted. Writes and
// piece hashing use the cached handle outside the lock, so closing it in
// rename or closeWriters while they ran made them fail with "file already
// closed", which disables a torrent's download. Now a handle is closed only
// when its last user is done. Files are opened with openFile, so renaming
// works on Windows while handles are still open.
type classicFileIo struct {
	mu      sync.Mutex
	writers map[string]*classicHandle
	closed  bool
}

type classicHandle struct {
	f        *os.File
	refs     int
	detached bool // removed from writers; closed when refs reaches 0
}

func newClassicFileIo() fileIo {
	return &classicFileIo{}
}

func (me *classicFileIo) Close() error {
	me.mu.Lock()
	defer me.mu.Unlock()
	_, _, err := me.closeWritersLocked()
	me.closed = true
	return err
}

func (me *classicFileIo) closeWriters() (closedPaths []string, remaining int, err error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return nil, 0, err
	}
	return me.closeWritersLocked()
}

func (me *classicFileIo) rename(from, to string) error {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return err
	}
	err := me.closeWriterLocked(from)
	err = errors.Join(err, me.closeWriterLocked(to))
	err = errors.Join(err, renameNoReplace(from, to))
	return err
}

func (me *classicFileIo) flush(name string, offset, nbytes int64) error {
	me.mu.Lock()
	if err := me.closedErr(); err != nil {
		me.mu.Unlock()
		return err
	}
	h := me.writers[name]
	if h == nil {
		me.mu.Unlock()
		// Gone means another piece already flushed and renamed the part file.
		if err := fsync(name); !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	h.refs++
	me.mu.Unlock()
	defer me.release(h)
	return h.f.Sync()
}

func (me *classicFileIo) openForSharedRead(name string) (sharableReader, error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return nil, err
	}
	return openFile(name, os.O_RDONLY, 0)
}

func (me *classicFileIo) openForRead(name string) (fileReader, error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return nil, err
	}
	if h, ok := me.writers[name]; ok {
		h.refs++
		return &classicHandleReader{io: me, h: h}, nil
	}
	f, err := openFile(name, os.O_RDONLY, 0)
	return classicFileReader{f}, err
}

func (me *classicFileIo) openForWrite(p string, size int64) (f fileWriter, err error) {
	me.mu.Lock()
	defer me.mu.Unlock()
	if err := me.closedErr(); err != nil {
		return nil, err
	}
	h, ok := me.writers[p]
	if !ok {
		file, err := openFileExtra(p, os.O_RDWR)
		if err != nil {
			return nil, err
		}
		if me.writers == nil {
			me.writers = make(map[string]*classicHandle)
		}
		h = &classicHandle{f: file}
		me.writers[p] = h
	}
	h.refs++
	return &classicFileWriter{File: h.f, io: me, h: h}, nil
}

// release drops one use of h, closing it if it was detached and now unused.
func (me *classicFileIo) release(h *classicHandle) {
	me.mu.Lock()
	defer me.mu.Unlock()
	h.refs--
	if h.refs == 0 && h.detached {
		h.f.Close()
	}
}

// closeWriterLocked removes name's cached handle, closing it now or, if it is
// in use, when its last user is done.
func (me *classicFileIo) closeWriterLocked(name string) error {
	h, ok := me.writers[name]
	if !ok {
		return nil
	}
	delete(me.writers, name)
	if h.refs > 0 {
		h.detached = true
		return nil
	}
	return h.f.Close()
}

func (me *classicFileIo) closeWritersLocked() (closedPaths []string, remaining int, err error) {
	closedPaths = make([]string, 0, len(me.writers))
	for name := range me.writers {
		closedPaths = append(closedPaths, name)
	}
	sort.Strings(closedPaths)
	for _, name := range closedPaths {
		err = errors.Join(err, me.closeWriterLocked(name))
	}
	return closedPaths, len(me.writers), err
}

func (me *classicFileIo) closedErr() error {
	if me.closed {
		return fs.ErrClosed
	}
	return nil
}

type classicFileWriter struct {
	*os.File
	io   *classicFileIo
	h    *classicHandle
	once sync.Once
}

func (w *classicFileWriter) Close() error {
	w.once.Do(func() { w.io.release(w.h) })
	return nil
}

type classicFileReader struct {
	*os.File
}

func (c classicFileReader) writeToN(w io.Writer, n int64) (written int64, err error) {
	lw := limitWriter{
		rem: n,
		w:   w,
	}
	written, err = c.File.WriteTo(&lw)
	// The limit stops the copy at n bytes; that is not a failed write.
	if errors.Is(err, io.ErrShortWrite) && lw.rem == 0 {
		err = nil
	}
	return
}

func (c classicFileReader) seekDataOrEof(offset int64) (ret int64, err error) {
	ret, err = seekData(c.File, offset)
	if err == io.EOF {
		ret, err = c.File.Seek(0, io.SeekEnd)
	}
	return
}

// classicHandleReader reads through a cached write handle (saving an open
// during piece hashing) and holds a reference so it is not closed underneath.
type classicHandleReader struct {
	io   *classicFileIo
	h    *classicHandle
	pos  int64
	once sync.Once
}

func (c *classicHandleReader) Close() error {
	c.once.Do(func() { c.io.release(c.h) })
	return nil
}

func (c *classicHandleReader) Read(p []byte) (n int, err error) {
	n, err = c.h.f.ReadAt(p, c.pos)
	c.pos += int64(n)
	if err == io.EOF && n > 0 {
		err = nil
	}
	return
}

func (c *classicHandleReader) writeToN(w io.Writer, n int64) (written int64, err error) {
	written, err = io.Copy(w, io.NewSectionReader(c.h.f, c.pos, n))
	c.pos += written
	return
}

func (c *classicHandleReader) seekDataOrEof(offset int64) (ret int64, err error) {
	ret, err = seekData(c.h.f, offset)
	if err == io.EOF {
		var fi os.FileInfo
		fi, err = c.h.f.Stat()
		if err != nil {
			return c.pos, err
		}
		ret = fi.Size()
		err = nil
	}
	if err != nil {
		return c.pos, err
	}
	c.pos = ret
	return
}
