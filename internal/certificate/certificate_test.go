package certificate

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"v2way/internal/store"
)

func TestEncryptedUploadAndMissingKey(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "app.example.com"}, DNSNames: []string{"app.example.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	private, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: private})
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	info, err := m.Upload("测试证书", string(certPEM), string(keyPEM))
	if err != nil {
		t.Fatal(err)
	}
	pair, err := m.Load(info.ID)
	if err != nil || pair.Leaf.VerifyHostname("app.example.com") != nil {
		t.Fatal("加密证书无法读取")
	}
	var raw []byte
	raw, err = os.ReadFile(filepath.Join(dir, "certificates", info.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, keyPEM) || bytes.Contains(raw, private) {
		t.Fatal("私钥明文写入文件")
	}
	metadata, err := m.List()
	if err != nil || len(metadata) != 1 {
		t.Fatal("证书列表失败")
	}
	if _, err = m.Upload("错误证书", string(certPEM), "bad key"); err == nil {
		t.Fatal("无效私钥被接受")
	}
	legacyID := "0123456789abcdef01234567"
	legacyInfo := info
	legacyInfo.ID = legacyID
	legacyMeta, _ := json.Marshal(legacyInfo)
	material, _ := json.Marshal(struct{ Cert, Key string }{string(certPEM), string(keyPEM)})
	nonce := make([]byte, m.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	secret := m.aead.Seal(nonce, nonce, material, []byte(legacyID))
	if err = m.ImportLegacy(legacyID, legacyMeta, []byte("bad")); err == nil {
		t.Fatal("损坏旧密文被接受")
	}
	if err = m.ImportLegacy(legacyID, legacyMeta, secret); err != nil {
		t.Fatal(err)
	}
	pair, err = m.Load(legacyID)
	if err != nil || pair.Leaf.VerifyHostname("app.example.com") != nil {
		t.Fatal("旧证书转换后不可用")
	}
	if err = m.ImportLegacy(legacyID, legacyMeta, secret); err != nil {
		t.Fatal("转换中断无法继续")
	}
	raw, err = os.ReadFile(filepath.Join(dir, "certificates", legacyID+".json"))
	if err != nil || bytes.Contains(raw, private) || bytes.Contains(raw, keyPEM) {
		t.Fatal("转换证书明文泄漏")
	}
	os.Remove(filepath.Join(dir, "master.key"))
	if _, err = Open(dir); err == nil {
		t.Fatal("丢失主密钥时静默替换")
	}
}
