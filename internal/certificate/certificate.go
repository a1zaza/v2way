package certificate

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"

	"v2way/internal/jsonfile"
	"v2way/internal/store"
)

type Info struct {
	ID          string    `json:"id"`
	Name        string    `json:"name"`
	Domains     []string  `json:"domains"`
	NotAfter    time.Time `json:"notAfter"`
	Fingerprint string    `json:"fingerprint"`
	NotBefore   time.Time `json:"notBefore"`
	Source      string    `json:"source,omitempty"`
	Environment string    `json:"environment,omitempty"`
	AutoRenew   bool      `json:"autoRenew"`
}
type Manager struct {
	aead  cipher.AEAD
	dir   string
	mu    sync.RWMutex
	cache map[string]*tls.Certificate
	auto  *Automation
}

var safeID = regexp.MustCompile(`^[a-f0-9]{24}$`)

type fileRecord struct {
	SchemaVersion int    `json:"schemaVersion"`
	Info          Info   `json:"info"`
	Secret        []byte `json:"secret"`
}

func Open(dir string) (*Manager, error) {
	path := filepath.Join(dir, "master.key")
	key, e := os.ReadFile(path)
	if errors.Is(e, os.ErrNotExist) {
		entries, err := os.ReadDir(filepath.Join(dir, "certificates"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		for _, entry := range entries {
			if filepath.Ext(entry.Name()) == ".json" {
				return nil, errors.New("主密钥缺失，需恢复原 master.key")
			}
		}
		entries, err = os.ReadDir(filepath.Join(dir, "acme"))
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
		if len(entries) > 0 {
			return nil, errors.New("ACME 数据存在但主密钥缺失，请恢复原 master.key")
		}
		key = make([]byte, 32)
		if _, e = rand.Read(key); e != nil {
			return nil, e
		}
		f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err != nil {
			return nil, err
		}
		_, e = f.Write(key)
		if e == nil {
			e = f.Sync()
		}
		ce := f.Close()
		if e != nil {
			return nil, e
		}
		if ce != nil {
			return nil, ce
		}
		parent, err := os.Open(dir)
		if err != nil {
			return nil, err
		}
		err = parent.Sync()
		parent.Close()
		if err != nil {
			return nil, err
		}
	} else if e != nil {
		return nil, e
	}
	if len(key) != 32 {
		return nil, errors.New("主密钥长度无效，请恢复原 master.key")
	}
	block, e := aes.NewCipher(key)
	if e != nil {
		return nil, e
	}
	aead, e := cipher.NewGCM(block)
	if e != nil {
		return nil, e
	}
	m := &Manager{aead: aead, dir: dir, cache: map[string]*tls.Certificate{}}
	m.auto, e = newAutomation(m)
	if e != nil {
		return nil, e
	}
	return m, nil
}
func (m *Manager) Upload(name, certPEM, keyPEM string) (Info, error) {
	return m.save(store.Token()[:24], name, certPEM, keyPEM, "manual", "", false)
}
func (m *Manager) save(id, name, certPEM, keyPEM, source, environment string, autoRenew bool) (Info, error) {
	pair, e := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if e != nil {
		return Info{}, errors.New("证书或私钥格式无效，或两者不匹配")
	}
	leaf, e := x509.ParseCertificate(pair.Certificate[0])
	if e != nil {
		return Info{}, errors.New("无法解析证书")
	}
	if time.Now().Before(leaf.NotBefore) || !time.Now().Before(leaf.NotAfter) {
		return Info{}, errors.New("证书尚未生效或已过期")
	}
	if len(leaf.DNSNames) == 0 && len(leaf.IPAddresses) == 0 {
		return Info{}, errors.New("证书必须包含域名或 IP 的 SAN 扩展")
	}
	if len(name) == 0 || len(name) > 128 {
		return Info{}, errors.New("证书名称应为 1–128 个字符")
	}
	hash := sha256.Sum256(leaf.Raw)
	info := Info{ID: id, Name: name, Domains: leaf.DNSNames, NotAfter: leaf.NotAfter, Fingerprint: hex.EncodeToString(hash[:]), NotBefore: leaf.NotBefore, Source: source, Environment: environment, AutoRenew: autoRenew}
	for _, ip := range leaf.IPAddresses {
		info.Domains = append(info.Domains, ip.String())
	}
	raw, _ := json.Marshal(struct {
		Cert string
		Key  string
	}{certPEM, keyPEM})
	nonce := make([]byte, m.aead.NonceSize())
	if _, e = rand.Read(nonce); e != nil {
		return Info{}, e
	}
	secret := m.aead.Seal(nonce, nonce, raw, []byte("certificate-v1:"+info.ID))
	m.mu.Lock()
	defer m.mu.Unlock()
	if e = jsonfile.Write(filepath.Join(m.dir, "certificates", info.ID+".json"), fileRecord{1, info, secret}); e != nil {
		return Info{}, e
	}
	pair.Leaf = leaf
	m.cache[id] = &pair
	return info, nil
}
func (m *Manager) Load(id string) (*tls.Certificate, error) {
	if !safeID.MatchString(id) {
		return nil, errors.New("证书标识无效")
	}
	m.mu.RLock()
	cached := m.cache[id]
	m.mu.RUnlock()
	if cached != nil {
		return cached, nil
	}
	var record fileRecord
	err := jsonfile.Read(filepath.Join(m.dir, "certificates", id+".json"), &record)
	if err == nil {
		if record.SchemaVersion != 1 || record.Info.ID != id {
			return nil, errors.New("证书文件格式无效")
		}
		raw, err := m.decrypt(record.Secret, "certificate-v1:"+id)
		if err != nil {
			return nil, err
		}
		var value struct {
			Cert string
			Key  string
		}
		if err = json.Unmarshal(raw, &value); err != nil {
			return nil, err
		}
		pair, err := tls.X509KeyPair([]byte(value.Cert), []byte(value.Key))
		if err != nil {
			return nil, err
		}
		pair.Leaf, err = x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, err
		}
		m.mu.Lock()
		if current := m.cache[id]; current != nil {
			m.mu.Unlock()
			return current, nil
		}
		m.cache[id] = &pair
		m.mu.Unlock()
		return &pair, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("证书文件损坏或无法读取")
	}
	return nil, errors.New("证书不存在")
}

func (m *Manager) List() ([]Info, error) {
	files := []Info{}
	entries, err := os.ReadDir(filepath.Join(m.dir, "certificates"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 || entry.IsDir() || filepath.Ext(entry.Name()) != ".json" {
			continue
		}
		var record fileRecord
		if err = jsonfile.Read(filepath.Join(m.dir, "certificates", entry.Name()), &record); err != nil {
			return nil, errors.New("证书文件损坏")
		}
		if record.SchemaVersion != 1 || !safeID.MatchString(record.Info.ID) || entry.Name() != record.Info.ID+".json" {
			return nil, errors.New("证书文件格式无效")
		}
		files = append(files, record.Info)
	}
	return files, nil
}

func (m *Manager) decrypt(secret []byte, aad string) ([]byte, error) {
	n := m.aead.NonceSize()
	if len(secret) < n {
		return nil, errors.New("加密材料损坏")
	}
	raw, err := m.aead.Open(nil, secret[:n], secret[n:], []byte(aad))
	if err != nil {
		return nil, errors.New("加密材料解密失败，请核对主密钥")
	}
	return raw, nil
}
func (m *Manager) writeSecret(kind, id string, value any) error {
	if !safeID.MatchString(id) {
		return errors.New("加密材料标识无效")
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	secret := m.aead.Seal(nonce, nonce, raw, []byte(kind+":"+id))
	return jsonfile.Write(filepath.Join(m.dir, "acme", kind, id+".json"), struct {
		Version int    `json:"version"`
		Secret  []byte `json:"secret"`
	}{1, secret})
}
func (m *Manager) readSecret(kind, id string, value any) error {
	if !safeID.MatchString(id) {
		return errors.New("加密材料标识无效")
	}
	var v struct {
		Version int    `json:"version"`
		Secret  []byte `json:"secret"`
	}
	if err := jsonfile.Read(filepath.Join(m.dir, "acme", kind, id+".json"), &v); err != nil {
		return err
	}
	if v.Version != 1 {
		return errors.New("加密文件版本不支持")
	}
	raw, err := m.decrypt(v.Secret, kind+":"+id)
	if err != nil {
		return err
	}
	return json.Unmarshal(raw, value)
}
