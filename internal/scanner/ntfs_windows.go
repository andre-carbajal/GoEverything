//go:build windows

package scanner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"

	"goeverything/internal/db"
)

const (
	fileDeviceFileSystem = 0x00000009
	methodBuffered       = 0
	methodNeither        = 3
	fileAnyAccess        = 0

	fsctlEnumUSNData     = fileDeviceFileSystem<<16 | fileAnyAccess<<14 | 44<<2 | methodNeither
	fsctlQueryUSNJournal = fileDeviceFileSystem<<16 | fileAnyAccess<<14 | 61<<2 | methodBuffered

	ntfsRecordV2HeaderSize = 60
	filetimeUnixOffset     = 116444736000000000
)

type ntfsBackend struct{}

type mftEnumDataV0 struct {
	StartFileReferenceNumber uint64
	LowUsn                   int64
	HighUsn                  int64
}

type usnJournalDataV0 struct {
	UsnJournalID    uint64
	FirstUsn        int64
	NextUsn         int64
	LowestValidUsn  int64
	MaxUsn          int64
	MaximumSize     uint64
	AllocationDelta uint64
}

type ntfsRecord struct {
	frn       uint64
	parent    uint64
	name      string
	attrs     uint32
	timestamp int64
	isDir     bool
}

type ntfsVolumePlan struct {
	root  string
	paths []string
}

func (b ntfsBackend) Scan(ctx context.Context, roots []string, emit func(db.Entry) error, progress scanProgress) error {
	plans, err := planNTFSVolumes(roots)
	if err != nil {
		return err
	}
	for _, plan := range plans {
		if err := scanNTFSVolume(ctx, plan, emit, progress); err != nil {
			return err
		}
	}
	return nil
}

func planNTFSVolumes(roots []string) ([]ntfsVolumePlan, error) {
	byVolume := map[string][]string{}
	for _, root := range roots {
		abs, err := filepath.Abs(root)
		if err != nil {
			return nil, err
		}
		clean := filepath.Clean(abs)
		volumeRoot, err := volumeRootForPath(clean)
		if err != nil {
			return nil, err
		}
		byVolume[volumeRoot] = append(byVolume[volumeRoot], clean)
	}

	plans := make([]ntfsVolumePlan, 0, len(byVolume))
	for volumeRoot, paths := range byVolume {
		if !isNTFSVolume(volumeRoot) {
			return nil, fmt.Errorf("%s is not an NTFS local volume", volumeRoot)
		}
		sort.Strings(paths)
		plans = append(plans, ntfsVolumePlan{root: volumeRoot, paths: paths})
	}
	sort.Slice(plans, func(i, j int) bool { return plans[i].root < plans[j].root })
	return plans, nil
}

func volumeRootForPath(path string) (string, error) {
	volume := filepath.VolumeName(path)
	if len(volume) != 2 || volume[1] != ':' {
		return "", fmt.Errorf("ntfs backend only supports drive-letter paths, got %q", path)
	}
	return strings.ToUpper(volume[:1]) + `:\`, nil
}

func isNTFSVolume(root string) bool {
	var fsName [32]uint16
	err := windows.GetVolumeInformation(
		windows.StringToUTF16Ptr(root),
		nil,
		0,
		nil,
		nil,
		nil,
		&fsName[0],
		uint32(len(fsName)),
	)
	if err != nil {
		return false
	}
	return strings.EqualFold(windows.UTF16ToString(fsName[:]), "NTFS")
}

func scanNTFSVolume(ctx context.Context, plan ntfsVolumePlan, emit func(db.Entry) error, progress scanProgress) error {
	handle, err := openVolume(plan.root)
	if err != nil {
		return err
	}
	defer func() { _ = windows.CloseHandle(handle) }()

	records, err := enumerateNTFSRecords(ctx, handle, progress)
	if err != nil {
		return err
	}

	pathByFRN := make(map[uint64]string, len(records))
	build := func(frn uint64) (string, bool) {
		return buildNTFSPath(plan.root, frn, records, pathByFRN, map[uint64]bool{})
	}

	for _, requestedRoot := range plan.paths {
		info, statErr := os.Stat(requestedRoot)
		if statErr != nil {
			continue
		}
		progress.CurrentPath.Store(requestedRoot)
		atomic.AddInt64(progress.Scanned, 1)
		progress.Emit()
		if err := emit(db.NewEntryFromPath(requestedRoot, requestedRoot, 0, info.ModTime(), true)); err != nil {
			return err
		}
	}

	for frn, record := range records {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if record.name == "" || record.name == "." {
			continue
		}
		path, ok := build(frn)
		if !ok || !pathWithinAnyRoot(path, plan.paths) {
			continue
		}
		root := matchingRoot(path, plan.paths)
		if root == "" {
			continue
		}
		progress.CurrentPath.Store(path)
		atomic.AddInt64(progress.Scanned, 1)
		progress.Emit()

		size := int64(0)
		mtime := ntfsFiletime(record.timestamp)
		isDir := record.isDir
		if info, statErr := os.Stat(path); statErr == nil {
			size = info.Size()
			mtime = info.ModTime()
			isDir = info.IsDir()
		}
		if err := emit(db.NewEntryFromPath(root, path, size, mtime, isDir)); err != nil {
			return err
		}
	}
	return nil
}

func openVolume(root string) (windows.Handle, error) {
	name := `\\.\` + strings.TrimSuffix(root, `\`)
	handle, err := windows.CreateFile(
		windows.StringToUTF16Ptr(name),
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return windows.InvalidHandle, fmt.Errorf("open NTFS volume %s: %w", root, err)
	}
	return handle, nil
}

func enumerateNTFSRecords(ctx context.Context, handle windows.Handle, progress scanProgress) (map[uint64]ntfsRecord, error) {
	var journal usnJournalDataV0
	var returned uint32
	if err := windows.DeviceIoControl(
		handle,
		fsctlQueryUSNJournal,
		nil,
		0,
		(*byte)(unsafe.Pointer(&journal)),
		uint32(unsafe.Sizeof(journal)),
		&returned,
		nil,
	); err != nil {
		return nil, fmt.Errorf("query USN journal: %w", err)
	}

	enum := mftEnumDataV0{LowUsn: journal.FirstUsn, HighUsn: journal.NextUsn}
	buf := make([]byte, 1<<20)
	records := make(map[uint64]ntfsRecord)
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		returned = 0
		err := windows.DeviceIoControl(
			handle,
			fsctlEnumUSNData,
			(*byte)(unsafe.Pointer(&enum)),
			uint32(unsafe.Sizeof(enum)),
			&buf[0],
			uint32(len(buf)),
			&returned,
			nil,
		)
		if err != nil {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
			return nil, fmt.Errorf("enumerate MFT: %w", err)
		}
		if returned <= 8 {
			break
		}

		enum.StartFileReferenceNumber = *(*uint64)(unsafe.Pointer(&buf[0]))
		offset := uint32(8)
		for offset+ntfsRecordV2HeaderSize <= returned {
			record, n, ok := parseNTFSRecord(buf[offset:returned])
			if !ok || n == 0 {
				atomic.AddInt64(progress.Skipped, 1)
				break
			}
			records[record.frn] = record
			offset += n
		}
	}
	return records, nil
}

func parseNTFSRecord(buf []byte) (ntfsRecord, uint32, bool) {
	if len(buf) < ntfsRecordV2HeaderSize {
		return ntfsRecord{}, 0, false
	}
	recordLength := *(*uint32)(unsafe.Pointer(&buf[0]))
	if recordLength < ntfsRecordV2HeaderSize || int(recordLength) > len(buf) {
		return ntfsRecord{}, 0, false
	}
	fileNameLength := *(*uint16)(unsafe.Pointer(&buf[56]))
	fileNameOffset := *(*uint16)(unsafe.Pointer(&buf[58]))
	nameEnd := uint32(fileNameOffset) + uint32(fileNameLength)
	if nameEnd > recordLength || fileNameLength%2 != 0 {
		return ntfsRecord{}, recordLength, false
	}
	nameBytes := buf[fileNameOffset:nameEnd]
	name := ""
	if len(nameBytes) > 0 {
		u16 := unsafe.Slice((*uint16)(unsafe.Pointer(&nameBytes[0])), len(nameBytes)/2)
		name = string(utf16.Decode(u16))
	}
	attrs := *(*uint32)(unsafe.Pointer(&buf[52]))
	return ntfsRecord{
		frn:       *(*uint64)(unsafe.Pointer(&buf[8])),
		parent:    *(*uint64)(unsafe.Pointer(&buf[16])),
		name:      name,
		attrs:     attrs,
		timestamp: *(*int64)(unsafe.Pointer(&buf[32])),
		isDir:     attrs&windows.FILE_ATTRIBUTE_DIRECTORY != 0,
	}, recordLength, true
}

func buildNTFSPath(volumeRoot string, frn uint64, records map[uint64]ntfsRecord, memo map[uint64]string, seen map[uint64]bool) (string, bool) {
	if path, ok := memo[frn]; ok {
		return path, true
	}
	if seen[frn] {
		return "", false
	}
	seen[frn] = true
	record, ok := records[frn]
	if !ok {
		return "", false
	}
	if record.parent == frn || record.name == "" || record.name == "." {
		memo[frn] = volumeRoot
		return volumeRoot, true
	}
	parentPath, ok := buildNTFSPath(volumeRoot, record.parent, records, memo, seen)
	if !ok {
		return "", false
	}
	path := filepath.Join(parentPath, record.name)
	memo[frn] = path
	return path, true
}

func pathWithinAnyRoot(path string, roots []string) bool {
	return matchingRoot(path, roots) != ""
}

func matchingRoot(path string, roots []string) string {
	cleanPath := strings.ToLower(filepath.Clean(path))
	for _, root := range roots {
		cleanRoot := strings.ToLower(filepath.Clean(root))
		if cleanPath == cleanRoot || strings.HasPrefix(cleanPath, strings.TrimRight(cleanRoot, `\`)+`\`) {
			return filepath.Clean(root)
		}
	}
	return ""
}

func ntfsFiletime(value int64) time.Time {
	if value <= filetimeUnixOffset {
		return time.Unix(0, 0)
	}
	return time.Unix(0, (value-filetimeUnixOffset)*100)
}
