package storage

import (
	"os"
	"unsafe"

	"github.com/edsrzf/mmap-go"
	"golang.org/x/sys/windows"
)

var pageSize = os.Getpagesize()

// msync writes one range of the mapping back to the file, like the Unix
// version. Magnetar patch: upstream flushed the whole mapping and then the
// whole file's disk buffers on every call, so each completed piece of a large
// file forced the entire file to disk.
func msync(mm mmap.MMap, offset, nbytes int) error {
	if nbytes <= 0 || offset >= len(mm) {
		return nil
	}
	start := offset - offset%pageSize
	end := min(offset+nbytes, len(mm))
	return windows.FlushViewOfFile(uintptr(unsafe.Pointer(&mm[start])), uintptr(end-start))
}
