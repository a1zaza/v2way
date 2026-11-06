package jsonfile

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestAtomicReadersNeverSeePartialJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "value.json")
	type state struct {
		Version int    `json:"version"`
		Value   string `json:"value"`
	}
	if err := Write(path, state{0, "ready"}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	errors := make(chan error, 1)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				var got state
				if err := Read(path, &got); err != nil {
					select {
					case errors <- err:
					default:
					}
					return
				}
			}
		}
	}()
	for i := 1; i <= 20; i++ {
		if err := Write(path, state{i, "ready"}); err != nil {
			t.Fatal(err)
		}
	}
	close(stop)
	wg.Wait()
	select {
	case err := <-errors:
		t.Fatalf("读到不完整 JSON: %v", err)
	default:
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("文件权限不正确")
	}
	var result state
	if err = Read(path, &result); err != nil || result.Version != 20 {
		t.Fatal("最终配置不正确")
	}
}
func TestMalformedAndTrailingJSONRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.json")
	for _, raw := range []string{`{"value":`, `{"value":"a"} {"value":"b"}`, `{"unknown":true}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		var value struct {
			Value string `json:"value"`
		}
		if Read(path, &value) == nil {
			t.Fatal("接受了无效 JSON")
		}
	}
}
