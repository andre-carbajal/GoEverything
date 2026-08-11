//go:build windows

package watcher

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"goeverything/internal/db"
)

const (
	fileListDirectory = 0x0001

	fileActionAdded          = 0x00000001
	fileActionRemoved        = 0x00000002
	fileActionModified       = 0x00000003
	fileActionRenamedOldName = 0x00000004
	fileActionRenamedNewName = 0x00000005
)

func (w *Watcher) Run(ctx context.Context, root string) error {
	if w.store == nil {
		return errors.New("watcher store is required")
	}
	if root == "" {
		return errors.New("watch root is required")
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return err
	}

	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(absRoot),
		fileListDirectory,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return fmt.Errorf("open watch root %q: %w", absRoot, err)
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	go func() {
		<-ctx.Done()
		_ = windows.CloseHandle(handle)
	}()

	buf := make([]byte, 64*1024)
	mask := uint32(windows.FILE_NOTIFY_CHANGE_FILE_NAME |
		windows.FILE_NOTIFY_CHANGE_DIR_NAME |
		windows.FILE_NOTIFY_CHANGE_ATTRIBUTES |
		windows.FILE_NOTIFY_CHANGE_SIZE |
		windows.FILE_NOTIFY_CHANGE_LAST_WRITE |
		windows.FILE_NOTIFY_CHANGE_CREATION)

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		var returned uint32
		err := windows.ReadDirectoryChanges(handle, &buf[0], uint32(len(buf)), true, mask, &returned, nil, 0)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, windows.ERROR_OPERATION_ABORTED) || errors.Is(err, windows.ERROR_INVALID_HANDLE) {
				return nil
			}
			return fmt.Errorf("read directory changes: %w", err)
		}
		if returned == 0 {
			continue
		}
		if err := w.applyWindowsNotifications(ctx, absRoot, buf[:returned]); err != nil {
			return err
		}
	}
}

func (w *Watcher) applyWindowsNotifications(ctx context.Context, root string, buf []byte) error {
	for offset := uint32(0); offset < uint32(len(buf)); {
		notification, ok := parseWindowsNotification(buf, offset)
		if !ok {
			return nil
		}
		path := filepath.Clean(filepath.Join(root, notification.name))
		switch notification.action {
		case fileActionRemoved, fileActionRenamedOldName:
			if err := deleteWatchedPathAndDescendants(ctx, w.store, path); err != nil {
				return err
			}
		case fileActionAdded, fileActionModified, fileActionRenamedNewName:
			if err := upsertChangedPath(ctx, w.store, root, path); err != nil {
				return err
			}
		}

		if notification.next == 0 {
			return nil
		}
		offset += notification.next
	}
	return nil
}

type windowsNotification struct {
	next   uint32
	action uint32
	name   string
}

func parseWindowsNotification(buf []byte, offset uint32) (windowsNotification, bool) {
	if int(offset)+12 > len(buf) {
		return windowsNotification{}, false
	}
	notification := windowsNotification{
		next:   *(*uint32)(unsafe.Pointer(&buf[offset])),
		action: *(*uint32)(unsafe.Pointer(&buf[offset+4])),
	}
	nameLen := *(*uint32)(unsafe.Pointer(&buf[offset+8]))
	nameStart := offset + 12
	nameEnd := nameStart + nameLen
	if nameEnd > uint32(len(buf)) || nameLen%2 != 0 {
		return windowsNotification{}, false
	}
	nameBytes := buf[nameStart:nameEnd]
	if len(nameBytes) > 0 {
		u16 := unsafe.Slice((*uint16)(unsafe.Pointer(&nameBytes[0])), len(nameBytes)/2)
		notification.name = string(utf16.Decode(u16))
	}
	return notification, true
}

func upsertChangedPath(ctx context.Context, store *db.Store, root, path string) error {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return nil
	}
	entry := db.NewEntryFromPath(root, path, info.Size(), info.ModTime(), info.IsDir())
	for attempt := 0; attempt < 3; attempt++ {
		if err := upsertWatchedEntries(ctx, store, []db.Entry{entry}); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(attempt+1) * 25 * time.Millisecond):
		}
	}
	return upsertWatchedEntries(ctx, store, []db.Entry{entry})
}
