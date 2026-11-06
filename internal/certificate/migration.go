package certificate

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"v2way/internal/jsonfile"
)

// ImportLegacy re-encrypts a legacy envelope using the current file AAD. Expired
// certificates are preserved for historical references; activation validates time.
func (m *Manager) ImportLegacy(id string, infoRaw json.RawMessage, secret []byte) error {
	if !safeID.MatchString(id) {
		return errors.New("旧证书标识无效")
	}
	var old Info
	if err := jsonfile.Decode(infoRaw, &old); err != nil || old.ID != id {
		return errors.New("旧证书元数据无效")
	}
	if _, err := os.Lstat(filepath.Join(m.dir, "certificates", id+".json")); err == nil {
		_, err = m.Load(id)
		return err
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := m.decrypt(secret, id)
	if err != nil {
		return err
	}
	var material struct {
		Cert string
		Key  string
	}
	if err = jsonfile.Decode(raw, &material); err != nil {
		return errors.New("旧证书材料损坏")
	}
	pair, err := tls.X509KeyPair([]byte(material.Cert), []byte(material.Key))
	if err != nil {
		return errors.New("旧证书私钥不匹配")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	hash := sha256.Sum256(leaf.Raw)
	if old.Fingerprint != "" && old.Fingerprint != hex.EncodeToString(hash[:]) {
		return errors.New("旧证书指纹不匹配")
	}
	info := Info{ID: id, Name: old.Name, Domains: leaf.DNSNames, NotBefore: leaf.NotBefore, NotAfter: leaf.NotAfter, Fingerprint: hex.EncodeToString(hash[:]), Source: "manual"}
	for _, ip := range leaf.IPAddresses {
		info.Domains = append(info.Domains, ip.String())
	}
	nonce := make([]byte, m.aead.NonceSize())
	if _, err = rand.Read(nonce); err != nil {
		return err
	}
	encrypted := m.aead.Seal(nonce, nonce, raw, []byte("certificate-v1:"+id))
	return jsonfile.Write(filepath.Join(m.dir, "certificates", id+".json"), fileRecord{1, info, encrypted})
}
