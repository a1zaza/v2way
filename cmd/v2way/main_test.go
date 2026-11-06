package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultStaticRoot(t *testing.T) {
	data := t.TempDir()
	roots, err := resolveStaticRoots(nil, data)
	if err != nil {
		t.Fatal(err)
	}
	want, err := filepath.EvalSymlinks("/www")
	if os.IsNotExist(err) {
		want = "/www"
	} else if err != nil {
		t.Fatal(err)
	}
	if len(roots) != 1 || roots[0] != want {
		t.Fatalf("默认静态根目录错误：%v", roots)
	}
}
func TestExplicitStaticRootsOverrideDefault(t *testing.T) {
	root, data := t.TempDir(), t.TempDir()
	other := t.TempDir()
	roots, err := resolveStaticRoots([]string{root, other}, data)
	if err != nil || len(roots) != 2 || roots[0] != root || roots[1] != other {
		t.Fatalf("显式根目录未覆盖默认值：%v %v", roots, err)
	}
	if _, err := resolveStaticRoots([]string{filepath.Join(root, "missing")}, data); err == nil {
		t.Fatal("显式不存在的目录未拒绝")
	}
}
func TestStaticRootsRejectDataOverlapAndSymlink(t *testing.T) {
	parent := t.TempDir()
	data := filepath.Join(parent, "data")
	if err := os.Mkdir(data, 0700); err != nil {
		t.Fatal(err)
	}
	child := filepath.Join(data, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(t.TempDir(), "alias")
	if err := os.Symlink(data, alias); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{parent, data, child, alias} {
		if _, err := resolveStaticRoots([]string{root}, data); err == nil {
			t.Fatalf("允许与数据目录重叠：%s", root)
		}
	}
}
