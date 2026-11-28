package storage

import (
	"os"

	"golang.org/x/sys/windows"
)

// openFile is os.OpenFile that also shares delete access and creates sparse
// files. Magnetar patch: Windows cannot rename a file while another handle
// without FILE_SHARE_DELETE is open, so finishing a part file failed whenever
// a piece was being hashed or uploaded to a peer.
func openFile(name string, flag int, perm os.FileMode) (*os.File, error) {
	p, err := windows.UTF16PtrFromString(name)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	var access uint32 = windows.GENERIC_READ
	if flag&(os.O_WRONLY|os.O_RDWR) != 0 {
		access |= windows.GENERIC_WRITE
	}
	var disp uint32
	switch {
	case flag&(os.O_CREATE|os.O_EXCL) == os.O_CREATE|os.O_EXCL:
		disp = windows.CREATE_NEW
	case flag&(os.O_CREATE|os.O_TRUNC) == os.O_CREATE|os.O_TRUNC:
		disp = windows.CREATE_ALWAYS
	case flag&os.O_CREATE != 0:
		disp = windows.OPEN_ALWAYS
	case flag&os.O_TRUNC != 0:
		disp = windows.TRUNCATE_EXISTING
	default:
		disp = windows.OPEN_EXISTING
	}
	attrs := uint32(windows.FILE_ATTRIBUTE_NORMAL)
	if flag&os.O_CREATE != 0 && perm&0o200 == 0 {
		attrs = windows.FILE_ATTRIBUTE_READONLY
	}
	h, err := windows.CreateFile(p, access,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, disp, attrs, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: name, Err: err}
	}
	if flag&os.O_CREATE != 0 && access&windows.GENERIC_WRITE != 0 {
		// Sparse, so writing a piece far into a new file does not first make
		// Windows fill everything before it with zeros (seconds per GB).
		// Drives without sparse files (FAT, exFAT) just ignore this.
		var n uint32
		_ = windows.DeviceIoControl(h, fsctlSetSparse, nil, 0, nil, 0, &n, nil)
	}
	return os.NewFile(uintptr(h), name), nil
}

const fsctlSetSparse = 0x000900c4
