package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"v2way/internal/jsonfile"
	"v2way/internal/store"
)

type fakeDNS struct {
	mu                 sync.Mutex
	values             map[string]string
	presented, cleaned int
	failPresent        bool
}

func (d *fakeDNS) Present(domain, token, keyAuth string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.presented++
	if strings.HasPrefix(domain, "*.") {
		return errors.New("DNS provider requires base domain")
	}
	if d.failPresent {
		return errors.New("provider rejected ultra-secret-token")
	}
	d.values[domain] = keyAuth
	return nil
}
func (d *fakeDNS) CleanUp(domain, token, keyAuth string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cleaned++
	delete(d.values, domain)
	return nil
}
func (d *fakeDNS) check(domain string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.values[domain] != ""
}

type fakeOrder struct {
	domains     []string
	valid       []bool
	certificate []byte
	finalized   bool
}
type fakeCA struct {
	server    *httptest.Server
	mu        sync.Mutex
	orders    []*fakeOrder
	validate  func(domain, token string) bool
	serial    int64
	finalizes int
	failFetch bool
	key       *ecdsa.PrivateKey
	issuer    *x509.Certificate
}

func newFakeCA(t *testing.T) *fakeCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	c := &fakeCA{key: key, issuer: &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(365 * 24 * time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}}
	c.server = httptest.NewServer(http.HandlerFunc(c.serve))
	t.Cleanup(c.server.Close)
	return c
}
func (c *fakeCA) serve(w http.ResponseWriter, r *http.Request) {
	c.mu.Lock()
	defer c.mu.Unlock()
	base := c.server.URL
	nonce := make([]byte, 24)
	rand.Read(nonce)
	w.Header().Set("Replay-Nonce", base64.RawURLEncoding.EncodeToString(nonce))
	w.Header().Set("Content-Type", "application/json")
	send := func(status int, value any) { w.WriteHeader(status); json.NewEncoder(w).Encode(value) }
	if r.URL.Path == "/directory" {
		send(200, map[string]any{"newNonce": base + "/nonce", "newAccount": base + "/account", "newOrder": base + "/new-order", "meta": map[string]string{"termsOfService": "https://example.test/terms"}})
		return
	}
	if r.URL.Path == "/nonce" {
		w.WriteHeader(204)
		return
	}
	var jws struct {
		Payload string `json:"payload"`
	}
	if err := json.NewDecoder(r.Body).Decode(&jws); err != nil {
		send(400, map[string]string{"detail": "bad JWS"})
		return
	}
	payload, _ := base64.RawURLEncoding.DecodeString(jws.Payload)
	if r.URL.Path == "/account" {
		w.Header().Set("Location", base+"/account/1")
		send(201, map[string]any{"status": "valid", "contact": []string{"mailto:admin@example.com"}})
		return
	}
	if r.URL.Path == "/new-order" {
		var request struct {
			Identifiers []struct {
				Value string `json:"value"`
			} `json:"identifiers"`
		}
		if json.Unmarshal(payload, &request) != nil {
			w.WriteHeader(400)
			return
		}
		o := &fakeOrder{}
		for _, id := range request.Identifiers {
			o.domains = append(o.domains, id.Value)
			o.valid = append(o.valid, false)
		}
		c.orders = append(c.orders, o)
		n := len(c.orders) - 1
		w.Header().Set("Location", fmt.Sprintf("%s/order/%d", base, n))
		send(201, c.order(n))
		return
	}
	var n, i int
	if _, err := fmt.Sscanf(r.URL.Path, "/auth/%d/%d", &n, &i); err == nil && strings.HasPrefix(r.URL.Path, "/auth/") {
		o := c.orders[n]
		domain := strings.TrimPrefix(o.domains[i], "*.")
		status := "pending"
		if o.valid[i] {
			status = "valid"
		}
		token := fmt.Sprintf("token-%d-%d", n, i)
		send(200, map[string]any{"identifier": map[string]string{"type": "dns", "value": domain}, "wildcard": strings.HasPrefix(o.domains[i], "*."), "status": status, "challenges": []map[string]string{{"type": "http-01", "url": fmt.Sprintf("%s/challenge/%d/%d", base, n, i), "token": token, "status": status}, {"type": "dns-01", "url": fmt.Sprintf("%s/challenge/%d/%d", base, n, i), "token": token, "status": status}}})
		return
	}
	if _, err := fmt.Sscanf(r.URL.Path, "/challenge/%d/%d", &n, &i); err == nil && strings.HasPrefix(r.URL.Path, "/challenge/") {
		o := c.orders[n]
		token := fmt.Sprintf("token-%d-%d", n, i)
		if c.validate != nil && !c.validate(strings.TrimPrefix(o.domains[i], "*."), token) {
			send(400, map[string]string{"type": "urn:ietf:params:acme:error:unauthorized", "detail": "challenge failed"})
			return
		}
		o.valid[i] = true
		send(200, map[string]string{"type": "dns-01", "url": r.URL.String(), "status": "valid", "token": token})
		return
	}
	if _, err := fmt.Sscanf(r.URL.Path, "/order/%d", &n); err == nil && strings.HasPrefix(r.URL.Path, "/order/") {
		send(200, c.order(n))
		return
	}
	if _, err := fmt.Sscanf(r.URL.Path, "/finalize/%d", &n); err == nil && strings.HasPrefix(r.URL.Path, "/finalize/") {
		var request struct {
			CSR string `json:"csr"`
		}
		json.Unmarshal(payload, &request)
		der, err := base64.RawURLEncoding.DecodeString(request.CSR)
		if err != nil {
			w.WriteHeader(400)
			return
		}
		csr, err := x509.ParseCertificateRequest(der)
		if err != nil || csr.CheckSignature() != nil {
			w.WriteHeader(400)
			return
		}
		o := c.orders[n]
		c.serial++
		leaf := &x509.Certificate{SerialNumber: big.NewInt(c.serial + 1), DNSNames: csr.DNSNames, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(90 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
		cert, err := x509.CreateCertificate(rand.Reader, leaf, c.issuer, csr.PublicKey, c.key)
		if err != nil {
			w.WriteHeader(500)
			return
		}
		o.certificate = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert})
		o.finalized = true
		c.finalizes++
		w.Header().Set("Location", fmt.Sprintf("%s/order/%d", base, n))
		send(200, c.order(n))
		return
	}
	if _, err := fmt.Sscanf(r.URL.Path, "/cert/%d", &n); err == nil && strings.HasPrefix(r.URL.Path, "/cert/") {
		if c.failFetch {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(200)
			io.WriteString(w, "broken certificate payload")
			return
		}
		w.Header().Set("Content-Type", "application/pem-certificate-chain")
		w.Write(c.orders[n].certificate)
		return
	}
	send(404, map[string]string{"detail": "unknown endpoint"})
}
func (c *fakeCA) order(n int) map[string]any {
	o := c.orders[n]
	status := "ready"
	for _, valid := range o.valid {
		if !valid {
			status = "pending"
		}
	}
	if o.finalized {
		status = "valid"
	}
	urls := []string{}
	for i := range o.domains {
		urls = append(urls, fmt.Sprintf("%s/auth/%d/%d", c.server.URL, n, i))
	}
	return map[string]any{"status": status, "authorizations": urls, "finalize": fmt.Sprintf("%s/finalize/%d", c.server.URL, n), "certificate": fmt.Sprintf("%s/cert/%d", c.server.URL, n)}
}
func testManager(t *testing.T) *Manager {
	t.Helper()
	dir := t.TempDir()
	s, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	m, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.auto.Close)
	return m
}
func requestFor(method string) IssueRequest {
	return IssueRequest{Name: "自动证书", Domains: []string{"app.example.com"}, Email: "admin@example.com", Challenge: method, Environment: "staging", AcceptTerms: true, AutoRenew: true}
}
func awaitJob(t *testing.T, a *Automation, id string) Job {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		for _, j := range a.Jobs() {
			if j.ID == id && j.Status != "queued" && j.Status != "running" {
				return j
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("任务未结束: %+v", a.Jobs())
	return Job{}
}
func TestHTTP01IssuanceAndTokenIsolation(t *testing.T) {
	m := testManager(t)
	ca := newFakeCA(t)
	m.auto.directory = func(string) string { return ca.server.URL + "/directory" }
	ca.validate = func(domain, token string) bool {
		r := httptest.NewRequest("GET", "http://"+domain+"/.well-known/acme-challenge/"+token, nil)
		w := httptest.NewRecorder()
		if !m.HTTPChallenge(w, r) || w.Code != 200 || !strings.HasPrefix(w.Body.String(), token+".") {
			return false
		}
		r.Host = "other.example.com"
		other := httptest.NewRecorder()
		m.HTTPChallenge(other, r)
		return other.Code == 404
	}
	m.auto.Start()
	j, err := m.auto.Issue(requestFor("http-01"))
	if err != nil {
		t.Fatal(err)
	}
	done := awaitJob(t, m.auto, j.ID)
	if done.Status != "succeeded" {
		t.Fatal(done.Message)
	}
	cert, err := m.Load(j.CertificateID)
	if err != nil || cert.Leaf.VerifyHostname("app.example.com") != nil {
		t.Fatal("签发证书无法加载")
	}
	m.auto.mu.Lock()
	remaining := len(m.auto.tokens)
	m.auto.mu.Unlock()
	if remaining != 0 {
		t.Fatal("HTTP 挑战令牌未清理")
	}
	info, err := m.List()
	if err != nil || len(info) != 1 || info[0].Source != "acme" {
		t.Fatal("ACME 证书元数据不正确")
	}
	if _, err = os.Stat(filepath.Join(m.dir, "acme", "accounts")); err != nil {
		t.Fatal(err)
	}
}
func TestDNS01WildcardCleanupAndRenewal(t *testing.T) {
	m := testManager(t)
	ca := newFakeCA(t)
	d := &fakeDNS{values: map[string]string{}}
	m.auto.directory = func(string) string { return ca.server.URL + "/directory" }
	m.auto.provider = func(string, map[string]string) (challenge.Provider, error) { return d, nil }
	m.auto.waitDNS = func(ctx context.Context, domain, keyAuth string) error {
		if !d.check(domain) {
			return errors.New("DNS 未添加")
		}
		return nil
	}
	ca.validate = func(domain, token string) bool { return d.check(domain) }
	m.auto.Start()
	r := requestFor("dns-01")
	r.Domains = []string{"example.com", "*.example.com"}
	r.DNSProvider = "cloudflare"
	r.Credentials = map[string]string{"apiToken": "ultra-secret-token"}
	j, err := m.auto.Issue(r)
	if err != nil {
		t.Fatal(err)
	}
	if done := awaitJob(t, m.auto, j.ID); done.Status != "succeeded" {
		t.Fatal(done.Message)
	}
	old, err := m.Load(j.CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	next, err := m.auto.Renew(j.CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	if done := awaitJob(t, m.auto, next.ID); done.Status != "succeeded" {
		t.Fatal(done.Message)
	}
	current, err := m.Load(j.CertificateID)
	if err != nil || current == old || current.Leaf.SerialNumber.Cmp(old.Leaf.SerialNumber) == 0 {
		t.Fatal("续期没有热切换证书")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.values) != 0 || d.presented != 4 || d.cleaned != 4 {
		t.Fatalf("DNS 未精确清理: %+v", d)
	}
	raw, err := os.ReadFile(filepath.Join(m.dir, "acme", "plans", j.CertificateID+".json"))
	if err != nil || strings.Contains(string(raw), "ultra-secret-token") {
		t.Fatal("DNS 凭据未加密")
	}
	jobs, _ := json.Marshal(m.auto.Jobs())
	if strings.Contains(string(jobs), "ultra-secret-token") {
		t.Fatal("任务 API 泄漏凭据")
	}
}
func TestDNSFailureCleansAndRedacts(t *testing.T) {
	m := testManager(t)
	ca := newFakeCA(t)
	d := &fakeDNS{values: map[string]string{}, failPresent: true}
	m.auto.directory = func(string) string { return ca.server.URL + "/directory" }
	m.auto.provider = func(string, map[string]string) (challenge.Provider, error) { return d, nil }
	m.auto.Start()
	r := requestFor("dns-01")
	r.DNSProvider = "cloudflare"
	r.Credentials = map[string]string{"apiToken": "ultra-secret-token"}
	j, err := m.auto.Issue(r)
	if err != nil {
		t.Fatal(err)
	}
	done := awaitJob(t, m.auto, j.ID)
	if done.Status != "failed" || strings.Contains(done.Message, "ultra-secret-token") {
		t.Fatalf("失败状态或脱敏错误: %+v", done)
	}
	if d.cleaned != 1 {
		t.Fatal("失败后没有尝试清理")
	}
}
func TestInterruptedJobRestoredAndPrivatePlan(t *testing.T) {
	m := testManager(t)
	j, err := m.auto.Issue(requestFor("http-01"))
	if err != nil {
		t.Fatal(err)
	}
	a, err := newAutomation(m)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if len(a.Jobs()) != 1 || a.Jobs()[0].Status != "interrupted" {
		t.Fatal("未恢复中断状态")
	}
	var saved Job
	if err = jsonfile.Read(filepath.Join(m.dir, "acme", "jobs", j.ID+".json"), &saved); err != nil || saved.Status != "interrupted" {
		t.Fatal("中断状态未落盘")
	}
}
func TestRequestValidationAndProviderCatalog(t *testing.T) {
	r := requestFor("http-01")
	r.Domains = []string{"*.example.com"}
	if _, err := normalizeRequest(r); err == nil {
		t.Fatal("HTTP-01 接受了通配符")
	}
	r = requestFor("dns-01")
	r.DNSProvider = "cloudflare"
	r.Credentials = map[string]string{"apiToken": "token", "baseURL": "https://evil.example"}
	if _, err := normalizeRequest(r); err == nil {
		t.Fatal("接受了未知凭据字段")
	}
	r = requestFor("http-01")
	r.AcceptTerms = false
	if _, err := normalizeRequest(r); err == nil {
		t.Fatal("未同意条款仍可签发")
	}
	for _, provider := range DNSProviders() {
		v := map[string]string{}
		for _, field := range provider.Fields {
			if field.Required {
				v[field.Key] = "test-value"
			}
		}
		if provider.ID == "rfc2136" {
			v["zone"] = "example.com"
			v["tsigSecret"] = base64.StdEncoding.EncodeToString([]byte("test-key"))
		}
		if _, err := newDNSProvider(provider.ID, v); err != nil {
			t.Errorf("%s 构造失败: %v", provider.ID, err)
		}
	}
}
func TestIssueRequestIDDoesNotCreateDuplicatePlans(t *testing.T) {
	m := testManager(t)
	r := requestFor("http-01")
	r.RequestID = "abcdef012345abcdef012345"
	first, err := m.auto.Issue(r)
	if err != nil {
		t.Fatal(err)
	}
	again, err := m.auto.Issue(r)
	if err != nil || again.ID != first.ID || again.CertificateID != first.CertificateID {
		t.Fatal("重试创建了重复任务")
	}
	r.Name = "不同参数"
	if _, err = m.auto.Issue(r); err == nil {
		t.Fatal("相同请求标识接受了不同参数")
	}
	files, err := os.ReadDir(filepath.Join(m.dir, "acme", "plans"))
	if err != nil || len(files) != 1 {
		t.Fatal("创建了重复计划文件")
	}
}
func TestUnknownFinalizationResumesExistingOrder(t *testing.T) {
	m := testManager(t)
	ca := newFakeCA(t)
	ca.failFetch = true
	m.auto.directory = func(string) string { return ca.server.URL + "/directory" }
	ca.validate = func(domain, token string) bool { return true }
	m.auto.Start()
	j, err := m.auto.Issue(requestFor("http-01"))
	if err != nil {
		t.Fatal(err)
	}
	if done := awaitJob(t, m.auto, j.ID); done.Status != "unknown" {
		t.Fatalf("最终签发结果不明时未标记 unknown: %+v", done)
	}
	ca.mu.Lock()
	count, finalized := len(ca.orders), ca.finalizes
	ca.failFetch = false
	ca.mu.Unlock()
	next, err := m.auto.Renew(j.CertificateID)
	if err != nil {
		t.Fatal(err)
	}
	if done := awaitJob(t, m.auto, next.ID); done.Status != "succeeded" {
		t.Fatal(done.Message)
	}
	ca.mu.Lock()
	defer ca.mu.Unlock()
	if len(ca.orders) != count || ca.finalizes != finalized {
		t.Fatal("结果未知时创建了重复订单或重复签发")
	}
}
