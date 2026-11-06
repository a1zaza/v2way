package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"v2way/internal/certificate"
	"v2way/internal/jsonfile"
	"v2way/internal/store"
)

func migrate(dir, file string) error {
	if file == "" {
		return errors.New("migrate 需要 -file 指定受保护的旧库导出文件")
	}
	f, err := os.Open(file)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	f.Close()
	if err != nil {
		return err
	}
	var bundle store.LegacyExport
	if err = jsonfile.DecodeLimit(raw, &bundle, 128<<20); err != nil {
		return errors.New("旧库导出文件格式无效或超过 128 MiB")
	}
	if len(bundle.Certificates) > 0 {
		info, err := os.Stat(filepath.Join(dir, "master.key"))
		if err != nil || info.Size() != 32 {
			return errors.New("转换旧证书需要原 master.key，请先恢复原密钥")
		}
	}
	s, err := store.BeginLegacyImport(dir, bundle)
	if err != nil {
		return err
	}
	defer s.Close()
	certs, err := certificate.Open(dir)
	if err != nil {
		return err
	}
	defer certs.Automation().Close()
	for _, old := range bundle.Certificates {
		if err = certs.ImportLegacy(old.ID, old.Info, old.Secret); err != nil {
			return fmt.Errorf("旧证书转换失败：%w", err)
		}
	}
	if err = s.FinishLegacyImport(bundle); err != nil {
		return err
	}
	fmt.Printf("JSON 数据转换完成：管理员、草稿、%d 个历史版本及 %d 张旧证书已保留；原数据库未删除，会话需重新登录。\n", len(bundle.Versions), len(bundle.Certificates))
	return nil
}
