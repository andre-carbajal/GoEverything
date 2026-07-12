//go:build windows

package tui

import (
	"errors"
	"fmt"
	"syscall"
	"unsafe"
)

const (
	foDelete          = 3
	fofSilent         = 0x0004
	fofNoConfirmation = 0x0010
	fofAllowUndo      = 0x0040
	fofNoErrorUI      = 0x0400
)

type shFileOpStruct struct {
	hwnd                  uintptr
	wFunc                 uint32
	pFrom                 *uint16
	pTo                   *uint16
	fFlags                uint16
	fAnyOperationsAborted int32
	hNameMappings         uintptr
	lpszProgressTitle     *uint16
}

var shFileOperationW = syscall.NewLazyDLL("shell32.dll").NewProc("SHFileOperationW")

func moveToTrash(path string) error {
	from, err := syscall.UTF16FromString(path)
	if err != nil {
		return err
	}
	from = append(from, 0)
	op := shFileOpStruct{
		wFunc:  foDelete,
		pFrom:  &from[0],
		fFlags: fofAllowUndo | fofNoConfirmation | fofSilent | fofNoErrorUI,
	}
	result, _, callErr := shFileOperationW.Call(uintptr(unsafe.Pointer(&op)))
	if result != 0 {
		if !errors.Is(syscall.Errno(0), callErr) {
			return fmt.Errorf("move to Recycle Bin: %w", callErr)
		}
		return fmt.Errorf("move to Recycle Bin failed with code %d", result)
	}
	if op.fAnyOperationsAborted != 0 {
		return fmt.Errorf("move to Recycle Bin was canceled")
	}
	return nil
}
