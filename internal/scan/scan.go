//go:build windows

package scan

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"fattrace/internal/model"
)

type Options struct {
	MaxDepth         int
	DedupeHardlinks  bool
	IncludeDirs      bool
	ErrorSampleLimit int
	// OnError receives every permission/IO error as it happens; the stats
	// samples remain capped by ErrorSampleLimit.
	OnError func(model.ScanError)
}

type childDir struct {
	name string
	frn  uint64
}

type walker struct {
	ctx      context.Context
	opts     Options
	emit     func(model.FileRecord) error
	stats    model.ScanStats
	buf      []byte
	seen     map[uint64]struct{}
	errLimit int
}

// Walk enumerates root recursively using NtQueryDirectoryFile. Reparse points are
// reported but never traversed, so junctions cannot create loops or double counts.
func Walk(ctx context.Context, root string, opts Options, emit func(model.FileRecord) error) (model.ScanStats, error) {
	var stats model.ScanStats
	abs, err := normalizeRoot(root)
	if err != nil {
		return stats, err
	}
	if strings.HasPrefix(abs, `\\`) {
		return stats, errors.New("UNC and network paths are not supported")
	}
	vol := filepath.VolumeName(abs)
	if len(vol) != 2 || vol[1] != ':' {
		return stats, fmt.Errorf("unsupported path %q", abs)
	}

	h, err := openDirHandle(abs)
	if err != nil {
		return stats, fmt.Errorf("open root %s: %w", abs, err)
	}
	info, err := handleInfo(h)
	syscall.CloseHandle(h)
	if err != nil {
		return stats, fmt.Errorf("identify root %s: %w", abs, err)
	}
	frn := uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)
	serial := info.VolumeSerialNumber

	errLimit := opts.ErrorSampleLimit
	if errLimit <= 0 {
		errLimit = 200
	}

	w := &walker{
		ctx:      ctx,
		opts:     opts,
		emit:     emit,
		buf:      make([]byte, queryBufferSize),
		errLimit: errLimit,
		stats: model.ScanStats{
			Root:            abs,
			Volume:          vol,
			VolumeSerial:    serial,
			DedupeHardlinks: opts.DedupeHardlinks,
			IsVolumeRoot:    strings.EqualFold(abs, volumeRoot(abs)),
		},
	}
	if opts.DedupeHardlinks {
		w.seen = make(map[uint64]struct{})
	}
	if opts.IncludeDirs && w.emit != nil {
		rootRec := model.FileRecord{
			FRN:       frn,
			ParentFRN: 0,
			Name:      abs,
			Attrs:     info.FileAttributes,
			IsDir:     true,
		}
		if err := w.emit(rootRec); err != nil {
			return w.stats, err
		}
	}
	if total, free, _, err := GetVolumeSpace(abs); err == nil {
		w.stats.VolumeTotal = total
		w.stats.VolumeFree = free
		w.stats.VolumeUsed = total - free
	}

	start := time.Now()
	err = w.walkDir(abs, frn, 0)
	w.stats.DurationMS = time.Since(start).Milliseconds()

	if w.stats.IsVolumeRoot && w.stats.VolumeUsed > 0 {
		cov := float64(w.stats.UniqueAllocBytes) / float64(w.stats.VolumeUsed) * 100
		if cov > 100 {
			cov = 100
		}
		w.stats.CoveragePct = float64(int(cov*100+0.5)) / 100
	}
	return w.stats, err
}

func normalizeRoot(root string) (string, error) {
	if root == "" {
		return "", errors.New("empty root")
	}
	if len(root) == 2 && root[1] == ':' {
		root += `\`
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func (w *walker) walkDir(dir string, dirFRN uint64, depth int) error {
	if err := w.ctx.Err(); err != nil {
		return err
	}
	if w.opts.MaxDepth > 0 && depth > w.opts.MaxDepth {
		return nil
	}

	h, err := openDirHandle(dir)
	if err != nil {
		w.recordError(dir, dirFRN, "open", err)
		return nil
	}
	w.stats.Dirs++

	children, err := w.enumerate(h, dir, dirFRN)
	syscall.CloseHandle(h)
	if err != nil {
		return err
	}

	for _, child := range children {
		if err := w.walkDir(filepath.Join(dir, child.name), child.frn, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) enumerate(h syscall.Handle, dir string, dirFRN uint64) ([]childDir, error) {
	var children []childDir
	restart := uintptr(1)

	for {
		if err := w.ctx.Err(); err != nil {
			return nil, err
		}
		var iosb ioStatusBlock
		status, _, _ := procNtQueryDirectoryFile.Call(
			uintptr(h),
			0,
			0,
			0,
			uintptr(unsafe.Pointer(&iosb)),
			uintptr(unsafe.Pointer(&w.buf[0])),
			uintptr(len(w.buf)),
			uintptr(fileIDBothDirectoryInformation),
			0,
			0,
			restart,
		)
		restart = 0

		st := uint32(status)
		if st == statusNoMoreFiles {
			return children, nil
		}
		if st != 0 {
			w.recordError(dir, dirFRN, "NtQueryDirectoryFile", ntStatusError(st))
			return children, nil
		}

		n := int(iosb.Information)
		if n <= 0 {
			return children, nil
		}

		for off := 0; ; {
			if off+fileInfoStructSize > n {
				break
			}
			info := (*fileIDBothDirInfo)(unsafe.Pointer(&w.buf[off]))
			nameLen := int(info.FileNameLength)
			nameStart := off + fileInfoStructSize
			if nameLen <= 0 || nameStart+nameLen > n {
				break
			}
			name := syscall.UTF16ToString(unsafe.Slice(
				(*uint16)(unsafe.Pointer(&w.buf[nameStart])),
				nameLen/2,
			))
			next := int(info.NextEntryOffset)

			if name != "." && name != ".." {
				if err := w.handleEntry(dir, dirFRN, name, info, &children); err != nil {
					return nil, err
				}
			}

			if next <= 0 {
				break
			}
			off += next
		}
	}
}

func (w *walker) handleEntry(dir string, dirFRN uint64, name string, info *fileIDBothDirInfo, children *[]childDir) error {
	attrs := info.FileAttributes

	if attrs&fileAttributeReparse != 0 {
		w.stats.ReparseSkipped++
		return nil
	}

	if attrs&fileAttributeDirectory != 0 {
		*children = append(*children, childDir{name: name, frn: uint64(info.FileID)})
		if w.opts.IncludeDirs && w.emit != nil {
			rec := model.FileRecord{
				FRN:       uint64(info.FileID),
				ParentFRN: dirFRN,
				Name:      name,
				Attrs:     attrs,
				IsDir:     true,
			}
			if err := w.emit(rec); err != nil {
				return err
			}
		}
		return nil
	}

	rec := model.FileRecord{
		FRN:       uint64(info.FileID),
		ParentFRN: dirFRN,
		Name:      name,
		Size:      info.EndOfFile,
		AllocSize: info.AllocationSize,
		MTime:     filetimeToTime(info.LastWriteTime),
		Attrs:     attrs,
	}

	w.stats.Files++
	w.stats.Bytes += info.EndOfFile
	w.stats.AllocBytes += info.AllocationSize
	if w.opts.DedupeHardlinks {
		if _, ok := w.seen[rec.FRN]; ok {
			w.stats.HardlinkDupes++
		} else {
			w.seen[rec.FRN] = struct{}{}
			w.stats.UniqueBytes += info.EndOfFile
			w.stats.UniqueAllocBytes += info.AllocationSize
		}
	} else {
		w.stats.UniqueBytes += info.EndOfFile
		w.stats.UniqueAllocBytes += info.AllocationSize
	}

	if w.emit != nil {
		if err := w.emit(rec); err != nil {
			return err
		}
	}
	return nil
}

func (w *walker) recordError(path string, frn uint64, op string, err error) {
	w.stats.Errors++
	e := model.ScanError{Path: path, FRN: frn, Op: op, Msg: err.Error()}
	if len(w.stats.ErrorSamples) < w.errLimit {
		w.stats.ErrorSamples = append(w.stats.ErrorSamples, e)
	}
	if w.opts.OnError != nil {
		w.opts.OnError(e)
	}
}

// FileID returns the NTFS file ID (FRN) for path without following reparse points.
func FileID(path string) (uint64, error) {
	h, err := openFileHandle(path)
	if err != nil {
		return 0, err
	}
	defer syscall.CloseHandle(h)
	frn, _, err := handleIdentity(h)
	return frn, err
}
