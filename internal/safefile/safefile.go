// Package safefile 整份替换文件：读的一方看到的是旧文件或新文件，不会是半截。
//
// 临时文件放在目标所在目录（同一个文件系统，改名才是原子的），名字以点开头并带随机串；改名之前出错就删掉它。
// 父目录由调用方自己建。WriteDurable 另外落盘，权限正好是给定的 mode（不受 umask 影响），给断电后不能丢的文件用。
package safefile

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
)

// tempPath 是替换 path 时用的临时文件：同一个目录，名字以点开头、带随机串。
func tempPath(path, token string) string {
	return filepath.Join(filepath.Dir(path), "."+filepath.Base(path)+"."+token+".tmp")
}

// WriteAtomic 把 path 整份换成 data；新文件的权限是 0666 减去 umask。
func WriteAtomic(path string, data []byte) error {
	return replace(path, data, 0, false)
}

// WriteDurable 把 path 整份换成 data，权限正好是 mode，并且落盘：改名前同步文件、改名后同步目录，断电后新内容还在。
func WriteDurable(path string, data []byte, mode os.FileMode) error {
	return replace(path, data, mode.Perm(), true)
}

// replace 的 mode 为 0 时按 0666 减去 umask 建文件，否则建好后改成正好是 mode。
func replace(path string, data []byte, mode os.FileMode, durable bool) error {
	token := make([]byte, 8)
	if _, err := rand.Read(token); err != nil {
		return err
	}
	create := os.FileMode(0o666)
	if mode != 0 {
		create = mode
	}
	tmp := tempPath(path, hex.EncodeToString(token))
	file, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_EXCL, create)
	if err != nil {
		return err
	}
	err = fill(file, data, mode, durable)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err == nil {
		err = os.Rename(tmp, path)
	}
	if err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if durable {
		return syncDir(filepath.Dir(path))
	}
	return nil
}

func fill(file *os.File, data []byte, mode os.FileMode, durable bool) error {
	if mode != 0 {
		if err := file.Chmod(mode); err != nil { // 不受 umask 影响
			return err
		}
	}
	if _, err := file.Write(data); err != nil {
		return err
	}
	if durable {
		return file.Sync()
	}
	return nil
}

func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return err
	}
	err = handle.Sync()
	if closeErr := handle.Close(); err == nil {
		err = closeErr
	}
	return err
}
