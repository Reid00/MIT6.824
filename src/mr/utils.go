package mr

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
)

type SchedulePhase uint8

const (
	MapPhase SchedulePhase = iota
	ReducePhase
	CompletePhase
)

func (s SchedulePhase) String() string {
	switch s {
	case MapPhase:
		return "MapPhase"
	case ReducePhase:
		return "ReducePhase"
	case CompletePhase:
		return "CompletePhase"
	default:
		panic(fmt.Sprintf("unexpected SchedulePhase %d", s))
	}
}

type JobType uint8

const (
	MapJob JobType = iota
	ReduceJob
	WaitJob
	CompleteJob
)

func (job JobType) String() string {
	switch job {
	case MapJob:
		return "MapJob"
	case ReduceJob:
		return "ReduceJob"
	case WaitJob:
		return "WaitJob"
	case CompleteJob:
		return "CompleteJob"
	default:
		panic(fmt.Sprintf("unexpected jobType %d", job))
	}
}

type TaskStatus uint8

const (
	Idle TaskStatus = iota
	Working
	Finished
)

func generateMapResultFileName(mapNumber, reduceNum int) string {
	return fmt.Sprintf("mr-%d-%d", mapNumber, reduceNum)
}

func generateReduceResultFileName(reduceNum int) string {
	return fmt.Sprintf("mr-out-%d", reduceNum)
}

// atomicWriteFile write to a temp file first, then we'll atomically replace the target file
// with the temp file.
func atomicWriteFile(filename string, r io.Reader) (err error) {
	dir, file := filepath.Split(filename)
	if dir == "" {
		// if dir is emtpy, set it to cwd
		dir = "."
	}

	// 创建一个临时文件，dir作为目录，file 作为文件的前缀，系统会随机生成后缀
	f, err := os.CreateTemp(dir, file)
	if err != nil {
		return fmt.Errorf("cannot create temp file: %v", err)
	}
	defer func() {
		if err != nil {
			// 如果发生任何错误，都不要留下临时文件
			_ = os.Remove(f.Name()) // yes, ignore the error, not much we can do about it.
		}
	}()
	// defer os.Remove(f.Name())

	defer f.Close()

	name := f.Name()
	if _, err = io.Copy(f, r); err != nil {
		return fmt.Errorf("io.Copy(%v, %v) error, %v", f, r, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("f.Close() error: %v", err)
	}

	// get the file mode from original file and use that for replacement file
	info, err := os.Stat(filename)
	if errors.Is(err, fs.ErrNotExist) {
		// do nothing; filename actually dones't exist
		// return fs.ErrNotExist
	} else if err != nil {
		return err
	} else {
		if err := os.Chmod(name, info.Mode()); err != nil {
			return fmt.Errorf("os.Chmod() error: %v", err)
		}
	}

	if err := os.Rename(name, filename); err != nil {
		return fmt.Errorf("os.Rename() error: %v", err)
	}
	return nil
}
