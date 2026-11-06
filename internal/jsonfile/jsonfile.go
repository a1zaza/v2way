// Package jsonfile provides bounded reads and durable replacements on local Unix filesystems.
package jsonfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

var ErrUncertain = errors.New("文件已替换但目录同步失败，结果需确认")

const MaxBytes = 8 << 20

func Read(path string, out any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (8<<20)+1))
	if err != nil {
		return err
	}
	if len(raw) > 8<<20 {
		return errors.New("JSON 文件超过 8 MiB")
	}
	return Decode(raw, out)
}
func Decode(raw []byte, out any) error {
	return DecodeLimit(raw, out, MaxBytes)
}
func DecodeLimit(raw []byte, out any, limit int) error {
	if len(raw) > limit {
		return errors.New("JSON 超过大小上限")
	}
	check := json.NewDecoder(bytes.NewReader(raw))
	check.UseNumber()
	if err := walk(check, 0); err != nil {
		return err
	}
	if _, err := check.Token(); err != io.EOF {
		return errors.New("JSON 含尾随内容")
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(out); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return errors.New("JSON 文件含尾随内容")
	}
	return nil
}
func walk(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON 嵌套超过 64 层")
	}
	token, err := d.Token()
	if err != nil {
		return err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delimiter {
	case '{':
		keys := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			s, ok := key.(string)
			if !ok {
				return errors.New("JSON 键无效")
			}
			if keys[s] {
				return errors.New("JSON 包含重复键")
			}
			keys[s] = true
			if err = walk(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := walk(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("JSON 分隔符无效")
	}
	_, err = d.Token()
	return err
}
func Sync(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	err = f.Sync()
	f.Close()
	if err != nil {
		return err
	}
	p, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer p.Close()
	return p.Sync()
}
func Write(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if len(raw) > MaxBytes {
		return errors.New("JSON 文件超过 8 MiB")
	}
	dir := filepath.Dir(path)
	if err = mkdirDurable(dir); err != nil {
		return err
	}
	f, err := os.CreateTemp(dir, ".write-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if err = f.Chmod(0600); err != nil {
		f.Close()
		return err
	}
	n, err := f.Write(raw)
	if err == nil && n != len(raw) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return err
	}
	parent, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	defer parent.Close()
	if err = parent.Sync(); err != nil {
		return fmt.Errorf("%w: %v", ErrUncertain, err)
	}
	return nil
}
func mkdirDurable(dir string) error {
	info, err := os.Stat(dir)
	if err == nil {
		if !info.IsDir() {
			return errors.New("目标父路径不是目录")
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	parent := filepath.Dir(dir)
	if parent == dir {
		return err
	}
	if err = mkdirDurable(parent); err != nil {
		return err
	}
	if err = os.Mkdir(dir, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	f, err := os.Open(parent)
	if err != nil {
		return err
	}
	defer f.Close()
	return f.Sync()
}
